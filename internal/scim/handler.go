package scim

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	protocol "github.com/elimity-com/scim"
	protocolErrors "github.com/elimity-com/scim/errors"
	"github.com/elimity-com/scim/optional"
	"github.com/elimity-com/scim/schema"
	"github.com/scim2/filter-parser/v2"

	"github.com/Busnes-app/kycalendar/internal/access"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/crypto"
	"github.com/Busnes-app/kycalendar/internal/sso"
	"github.com/Busnes-app/kycalendar/internal/store"
)

type Server struct {
	config   config.SCIMConfig
	protocol http.Handler
}

func NewServer(st store.Store, cfg config.SCIMConfig, appURL string) *Server {
	userHandler := &userResourceHandler{store: st}
	groupHandler := &groupResourceHandler{store: st}
	server, err := protocol.NewServer(&protocol.ServerArgs{
		ServiceProviderConfig: &protocol.ServiceProviderConfig{
			DocumentationURI: optional.NewString("https://busnes.app/docs/scim"),
			SupportPatch:     true, SupportFiltering: true, MaxResults: 200,
			AuthenticationSchemes: []protocol.AuthenticationScheme{{Type: protocol.AuthenticationTypeOauthBearerToken, Name: "OAuth Bearer Token", Description: "RFC 6750 bearer token", SpecURI: optional.NewString("https://www.rfc-editor.org/rfc/rfc6750"), Primary: true}},
		},
		ResourceTypes: []protocol.ResourceType{
			{ID: optional.NewString("User"), Name: "User", Endpoint: "/Users", Description: optional.NewString("User Account"), Schema: schema.CoreUserSchema(), Handler: userHandler},
			{ID: optional.NewString("Group"), Name: "Group", Endpoint: "/Groups", Description: optional.NewString("Group Resource"), Schema: schema.CoreGroupSchema(), Handler: groupHandler},
		},
	}, protocol.WithBaseURL(strings.TrimRight(appURL, "/")+"/scim/v2"))
	if err != nil {
		return &Server{config: cfg, protocol: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "SCIM initialization failed", http.StatusInternalServerError)
		})}
	}
	return &Server{config: cfg, protocol: server}
}

// RegisterRoutes mounts the SCIM endpoints through handle, which records them.
func (s *Server) RegisterRoutes(handle func(pattern string, h http.Handler)) {
	h := http.StripPrefix("/scim", s.protocol)
	handle("/scim/v2", h)
	handle("/scim/v2/", h)
}

func (s *Server) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/scim/v2") {
			next.ServeHTTP(w, r)
			return
		}
		if !s.config.Enabled {
			writeAuthError(w, http.StatusForbidden, "SCIM provisioning is disabled")
			return
		}
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !constantTimeEqual(parts[1], s.config.BearerToken) {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			writeAuthError(w, http.StatusUnauthorized, "Invalid or missing bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func constantTimeEqual(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func writeAuthError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/scim+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(protocolErrors.ScimError{Status: status, Detail: detail})
}

type userResourceHandler struct{ store store.Store }

func (h *userResourceHandler) Create(r *http.Request, attrs protocol.ResourceAttributes) (protocol.Resource, error) {
	username, _ := attrs["userName"].(string)
	// A new row belongs to the current binding; replace and patch never move it to another.
	bound, err := sso.Bound(r.Context(), h.store.Settings())
	if err != nil {
		return protocol.Resource{}, err
	}
	// KyIdentity's externalId is the KySignOn sub: a user who signed in first is this user, if
	// they signed in through the bound issuer. Another issuer's row is never adopted (409).
	if ext := stringValue(attrs, "externalId", ""); ext != "" {
		existing, err := h.store.Users().GetUserBySSO(r.Context(), "kysignon", ext)
		if err == nil && existing.SSOIssuer != bound {
			return protocol.Resource{}, protocolErrors.ScimErrorUniqueness
		}
		if err == nil {
			res, err := h.Replace(r, existing.ID, attrs)
			if err == nil {
				_ = h.store.Audit().LogAudit(r.Context(), &store.AuditRecord{UserID: existing.ID, Action: "scim.user.adopt", Resource: username})
			}
			return res, err
		}
		if !errors.Is(err, store.ErrNotFound) {
			return protocol.Resource{}, err
		}
	}
	user := &store.User{ID: "usr_" + crypto.RandomHex(12), Username: username, Email: primaryValue(attrs["emails"]), DisplayName: stringValue(attrs, "displayName", username), Role: roleFromSCIM(attrs["roles"]), Status: statusFromActive(attrs), SSOProvider: "scim", SSOSubject: stringValue(attrs, "externalId", ""), SSOIssuer: bound}
	if err := h.store.Users().CreateUser(r.Context(), user); err != nil {
		return protocol.Resource{}, scimStoreError(err, user.ID)
	}
	_ = h.store.Audit().LogAudit(r.Context(), &store.AuditRecord{UserID: user.ID, Action: "scim.user.create", Resource: user.Username})
	return userResource(user), nil
}

// scimUser loads an account SCIM may see. A local account is not the IdP's: it reads as
// missing, so a reconciling IdP never demotes, disables or deletes the way back in.
func (h *userResourceHandler) scimUser(r *http.Request, id string) (*store.User, error) {
	user, err := h.store.Users().GetUserByID(r.Context(), id)
	if err == nil && user.SSOProvider == "local" {
		err = store.ErrNotFound
	}
	if err != nil {
		return nil, scimStoreError(err, id)
	}
	return user, nil
}

func (h *userResourceHandler) Get(r *http.Request, id string) (protocol.Resource, error) {
	user, err := h.scimUser(r, id)
	if err != nil {
		return protocol.Resource{}, err
	}
	return userResource(user), nil
}

// eqFilter is the one filter shape IdPs send to look a resource up: `<attr> eq "<value>"`, the
// value escaping `"` and `\` with a backslash.
var eqFilter = regexp.MustCompile(`^\s*([A-Za-z][\w.]*)\s+(?i:eq)\s+"((?:[^"\\]|\\.)*)"\s*$`)

const maxFilterValue = 1024

// parseEqFilter reads an eq filter; anything else, an escape other than `\"` or `\\`, or a
// value over maxFilterValue bytes is invalidFilter.
func parseEqFilter(raw string) (attr, value string, err error) {
	m := eqFilter.FindStringSubmatch(raw)
	if m == nil {
		return "", "", protocolErrors.ScimErrorInvalidFilter
	}
	var b strings.Builder
	for i := 0; i < len(m[2]); i++ {
		c := m[2][i]
		if c == '\\' {
			i++
			if c = m[2][i]; c != '"' && c != '\\' {
				return "", "", protocolErrors.ScimErrorInvalidFilter
			}
		}
		b.WriteByte(c)
	}
	if b.Len() > maxFilterValue {
		return "", "", protocolErrors.ScimErrorInvalidFilter
	}
	return m[1], b.String(), nil
}

var filterFields = map[string]store.UserField{
	"username":     store.UserFieldUsername,
	"emails.value": store.UserFieldEmail,
	"emails":       store.UserFieldEmail,
	"email":        store.UserFieldEmail,
	"displayname":  store.UserFieldDisplayName,
}

func (h *userResourceHandler) GetAll(r *http.Request, params protocol.ListRequestParams) (protocol.Page, error) {
	filter := store.UserFilter{SSOOnly: true}
	if raw := r.URL.Query().Get("filter"); raw != "" {
		attr, value, err := parseEqFilter(raw)
		if err != nil {
			return protocol.Page{}, err
		}
		field, ok := filterFields[strings.ToLower(attr)]
		if !ok && !strings.EqualFold(attr, "externalId") {
			return protocol.Page{}, protocolErrors.ScimErrorInvalidFilter
		}
		filter.Field, filter.Value = field, value
		if strings.EqualFold(attr, "externalId") {
			// KyIdentity's lookup of the account it provisions: its user ID is the sub. Exact, and
			// only rows SCIM may adopt (KyIdentity's providers, the current binding), so a lookup
			// never hands KyIdentity another binding's account.
			bound, err := sso.Bound(r.Context(), h.store.Settings())
			if err != nil {
				return protocol.Page{}, err
			}
			filter.Field, filter.Issuer, filter.Providers = store.UserFieldSSOSubject, bound, sso.AccountProviders(sso.KindKyIdentity)
		}
	}
	users, total, err := h.store.Users().ListUsers(r.Context(), params.StartIndex-1, params.Count, filter)
	if err != nil {
		return protocol.Page{}, err
	}
	resources := make([]protocol.Resource, 0, len(users))
	for _, user := range users {
		resources = append(resources, userResource(user))
	}
	return protocol.Page{TotalResults: total, Resources: resources}, nil
}

func (h *userResourceHandler) Replace(r *http.Request, id string, attrs protocol.ResourceAttributes) (protocol.Resource, error) {
	user, err := h.scimUser(r, id)
	if err != nil {
		return protocol.Resource{}, err
	}
	expectedRole, expectedStatus := user.Role, user.Status
	user.Username, _ = attrs["userName"].(string)
	user.Email = primaryValue(attrs["emails"])
	user.DisplayName = stringValue(attrs, "displayName", user.Username)
	user.Role = roleFromSCIM(attrs["roles"]) // PUT replaces: no roles means no admin grant
	user.Status = statusFromActive(attrs)
	return h.saveAndRead(r, user, expectedRole, expectedStatus)
}

// Delete checks then deletes: a row's provider never changes, so it cannot become local between.
func (h *userResourceHandler) Delete(r *http.Request, id string) error {
	if _, err := h.scimUser(r, id); err != nil {
		return err
	}
	return scimStoreError(h.store.Users().DeleteUser(r.Context(), id), id)
}

func (h *userResourceHandler) Patch(r *http.Request, id string, operations []protocol.PatchOperation) (protocol.Resource, error) {
	user, err := h.scimUser(r, id)
	if err != nil {
		return protocol.Resource{}, err
	}
	expectedRole, expectedStatus := user.Role, user.Status
	for _, op := range operations {
		if op.Path == nil {
			if values, ok := op.Value.(map[string]interface{}); ok && op.Op != protocol.PatchOperationRemove {
				applyUserValues(user, values)
			}
			continue
		}
		// Match on the attribute name, so "urn:...:User:roles" and "roles[value eq ...]" count.
		attr := strings.ToLower(op.Path.AttributePath.AttributeName)
		if op.Op == protocol.PatchOperationRemove {
			if attr == "roles" || attr == "role" {
				user.Role = "user" // the role is single-valued: removing any value removes the grant
			}
			continue
		}
		if op.Path.ValueExpression == nil && op.Path.SubAttribute == nil && op.Path.AttributePath.SubAttribute == nil {
			applyUserValue(user, attr, op.Value)
		}
	}
	return h.saveAndRead(r, user, expectedRole, expectedStatus)
}

// saveAndRead saves user and answers with the account as stored, which a concurrent change may
// have made differ from the request.
func (h *userResourceHandler) saveAndRead(r *http.Request, user *store.User, expectedRole, expectedStatus string) (protocol.Resource, error) {
	if err := h.save(r, user, expectedRole, expectedStatus); err != nil {
		return protocol.Resource{}, scimStoreError(err, user.ID)
	}
	stored, err := h.store.Users().GetUserByID(r.Context(), user.ID)
	if err != nil {
		return protocol.Resource{}, scimStoreError(err, user.ID)
	}
	return userResource(stored), nil
}

// save stores user through UpdateSCIMUser against the role and status the handler read: SCIM
// writes only the access it changes; a removal always lands; a grant lands only if nothing changed
// the account since SCIM read it, otherwise the IdP is asked to retry (412).
func (h *userResourceHandler) save(r *http.Request, user *store.User, expectedRole, expectedStatus string) error {
	return h.store.Users().UpdateSCIMUser(r.Context(), user, expectedRole, expectedStatus)
}

// roleFromSCIM is "admin" only when the IdP sends the KyCalendar app role; any other value,
// including KyIdentity's global "admin", is an everyday user.
func roleFromSCIM(value interface{}) string {
	if access.IsAdmin(access.RoleValues(value)) {
		return "admin"
	}
	return "user"
}

func applyUserValues(user *store.User, values map[string]interface{}) {
	for key, value := range values {
		applyUserValue(user, strings.ToLower(key), value)
	}
}
func applyUserValue(user *store.User, path string, value interface{}) {
	switch path {
	case "active":
		if active, ok := value.(bool); ok {
			if active {
				user.Status = "active"
			} else {
				user.Status = "inactive"
			}
		}
	case "displayname":
		if v, ok := value.(string); ok {
			user.DisplayName = v
		}
	case "username":
		if v, ok := value.(string); ok {
			user.Username = v
		}
	case "roles", "role":
		user.Role = roleFromSCIM(value)
	case "emails":
		user.Email = primaryValue(value)
	}
}

func userResource(user *store.User) protocol.Resource {
	attrs := protocol.ResourceAttributes{"userName": user.Username, "displayName": user.DisplayName, "active": user.Status == "active"}
	if user.Email != "" {
		attrs["emails"] = []interface{}{map[string]interface{}{"value": user.Email, "type": "work", "primary": true}}
	}
	if user.Role == "admin" {
		attrs["roles"] = []interface{}{map[string]interface{}{"value": access.AdminAppRole, "primary": true}}
	}
	return protocol.Resource{ID: user.ID, ExternalID: optional.NewString(user.SSOSubject), Attributes: attrs, Meta: protocol.Meta{Created: &user.CreatedAt, LastModified: &user.UpdatedAt}}
}

type groupResourceHandler struct{ store store.Store }

// ConflictKey names the setting that flags a SCIM group name a local group already holds, so the
// Groups screen can ask an admin to rename the local group. Hashed: names outgrow the key column.
func ConflictKey(name string) string {
	return "scim_group_conflict:" + crypto.SHA256Hex([]byte(strings.ToLower(name)))
}

// scimGroup loads a group SCIM owns. A local group is not SCIM's to read or change, so it reads
// as missing: an IdP reconciling its directory must never rename, empty or delete one.
func (h *groupResourceHandler) scimGroup(r *http.Request, id string) (*store.Group, error) {
	group, err := h.store.Groups().GetGroupByID(r.Context(), id)
	if err == nil && group.Source != store.GroupSourceSCIM {
		err = store.ErrNotFound
	}
	if err != nil {
		return nil, scimStoreError(err, id)
	}
	if group.Members, err = h.store.Users().SSOUserIDs(r.Context(), group.Members); err != nil {
		return nil, err
	}
	return group, nil
}

// scimMembers checks the members a SCIM request names. Each must be an account SCIM can see: a
// local account (or none) is refused before anything is written, so SCIM never grants one a
// group's calendars. Local memberships a group already holds are not SCIM's and stay.
func (h *groupResourceHandler) scimMembers(r *http.Request, named []string) ([]string, error) {
	ids := slices.Compact(slices.Sorted(slices.Values(named)))
	found, err := h.store.Users().SSOUserIDs(r.Context(), ids)
	if err != nil {
		return nil, err
	}
	if len(found) != len(ids) {
		return nil, protocolErrors.ScimErrorInvalidValue
	}
	return ids, nil
}

func (h *groupResourceHandler) Create(r *http.Request, attrs protocol.ResourceAttributes) (protocol.Resource, error) {
	members, err := h.scimMembers(r, memberValues(attrs["members"]))
	if err != nil {
		return protocol.Resource{}, err
	}
	group := &store.Group{ID: "grp_" + crypto.RandomHex(12), DisplayName: stringValue(attrs, "displayName", ""), ExternalID: stringValue(attrs, "externalId", ""), Source: store.GroupSourceSCIM}
	if err := h.store.Groups().CreateGroup(r.Context(), group); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			h.flagLocalTwin(r, group.DisplayName)
		}
		return protocol.Resource{}, scimStoreError(err, group.ID)
	}
	if err := h.replaceMembers(r, group.ID, nil, members); err != nil {
		return protocol.Resource{}, err
	}
	group.Members = members
	return groupResource(group), nil
}

// flagLocalTwin records that a local group holds name, so SCIM could not create it.
func (h *groupResourceHandler) flagLocalTwin(r *http.Request, name string) {
	if g, err := h.store.Groups().GetGroupByName(r.Context(), name); err == nil && g.Source == store.GroupSourceLocal {
		_ = h.store.Settings().SetSetting(r.Context(), ConflictKey(name), time.Now().UTC().Format(time.RFC3339))
	}
}

func (h *groupResourceHandler) Get(r *http.Request, id string) (protocol.Resource, error) {
	group, err := h.scimGroup(r, id)
	if err != nil {
		return protocol.Resource{}, err
	}
	return groupResource(group), nil
}

func (h *groupResourceHandler) GetAll(r *http.Request, params protocol.ListRequestParams) (protocol.Page, error) {
	var groups []*store.Group
	var total int
	var err error
	if raw := r.URL.Query().Get("filter"); raw != "" {
		// The one Groups filter: KyIdentity's lookup of a group it provisions.
		attr, value, perr := parseEqFilter(raw)
		if perr != nil || !strings.EqualFold(attr, "externalId") {
			return protocol.Page{}, protocolErrors.ScimErrorInvalidFilter
		}
		// Exact, SCIM groups only; a lookup is never more than a page.
		groups, err = h.store.Groups().GroupsByExternalID(r.Context(), value, store.GroupSourceSCIM)
		total = len(groups)
		if start := min(max(params.StartIndex-1, 0), len(groups)); err == nil {
			groups = groups[start:]
			if params.Count >= 0 && params.Count < len(groups) {
				groups = groups[:params.Count]
			}
		}
	} else {
		groups, total, err = h.store.Groups().ListGroups(r.Context(), params.StartIndex-1, params.Count, store.GroupSourceSCIM)
	}
	if err != nil {
		return protocol.Page{}, err
	}
	resources := make([]protocol.Resource, 0, len(groups))
	for _, group := range groups {
		if group.Members, err = h.store.Users().SSOUserIDs(r.Context(), group.Members); err != nil {
			return protocol.Page{}, err
		}
		resources = append(resources, groupResource(group))
	}
	return protocol.Page{TotalResults: total, Resources: resources}, nil
}
func (h *groupResourceHandler) Replace(r *http.Request, id string, attrs protocol.ResourceAttributes) (protocol.Resource, error) {
	group, err := h.scimGroup(r, id)
	if err != nil {
		return protocol.Resource{}, err
	}
	members, err := h.scimMembers(r, memberValues(attrs["members"]))
	if err != nil {
		return protocol.Resource{}, err
	}
	group.DisplayName = stringValue(attrs, "displayName", group.DisplayName)
	group.ExternalID = stringValue(attrs, "externalId", group.ExternalID)
	return h.save(r, group, members)
}

// save writes group and moves its SCIM-visible members (group.Members) to members; local
// memberships are never in either list, so they stay.
func (h *groupResourceHandler) save(r *http.Request, group *store.Group, members []string) (protocol.Resource, error) {
	if err := h.store.Groups().UpdateGroup(r.Context(), group); err != nil {
		return protocol.Resource{}, scimStoreError(err, group.ID)
	}
	if err := h.replaceMembers(r, group.ID, group.Members, members); err != nil {
		return protocol.Resource{}, err
	}
	group.Members = members
	return groupResource(group), nil
}

func (h *groupResourceHandler) Delete(r *http.Request, id string) error {
	if _, err := h.scimGroup(r, id); err != nil {
		return err
	}
	return scimStoreError(h.store.Groups().DeleteGroup(r.Context(), id), id)
}

// Patch applies RFC 7644 3.5.2 to members: add appends (duplicates ignored), remove with
// `members[value eq "<id>"]` drops that member, remove of `members` drops all, replace replaces.
// displayName and externalId are replaced; other attributes are ignored.
func (h *groupResourceHandler) Patch(r *http.Request, id string, operations []protocol.PatchOperation) (protocol.Resource, error) {
	group, err := h.scimGroup(r, id)
	if err != nil {
		return protocol.Resource{}, err
	}
	p := groupPatch{group: group, members: slices.Clone(group.Members)}
	for _, op := range operations {
		if err := p.apply(op); err != nil {
			return protocol.Resource{}, err
		}
	}
	if _, err := h.scimMembers(r, p.named); err != nil {
		return protocol.Resource{}, err
	}
	return h.save(r, group, slices.Sorted(slices.Values(p.members)))
}

// groupPatch is a group being patched: members are the SCIM-visible ones, named every member
// an add or replace asked for, all checked before anything is written.
type groupPatch struct {
	group   *store.Group
	members []string
	named   []string
}

func (p *groupPatch) apply(op protocol.PatchOperation) error {
	if op.Path == nil {
		values, _ := op.Value.(map[string]interface{})
		for key, value := range values {
			p.set(op.Op, strings.ToLower(key), value)
		}
		return nil
	}
	attr := strings.ToLower(op.Path.AttributePath.AttributeName)
	if attr != "members" {
		if op.Op != protocol.PatchOperationRemove && op.Path.ValueExpression == nil && op.Path.SubAttribute == nil && op.Path.AttributePath.SubAttribute == nil {
			p.set(op.Op, attr, op.Value)
		}
		return nil
	}
	switch {
	case op.Path.SubAttribute != nil || op.Path.AttributePath.SubAttribute != nil:
		return protocolErrors.ScimErrorInvalidPath
	case op.Path.ValueExpression != nil:
		id, ok := memberFilterID(op.Path.ValueExpression)
		if !ok || op.Op != protocol.PatchOperationRemove {
			return protocolErrors.ScimErrorInvalidFilter
		}
		p.members = slices.DeleteFunc(p.members, func(m string) bool { return m == id })
	case op.Op == protocol.PatchOperationRemove:
		p.members = nil
	default:
		p.set(op.Op, attr, op.Value)
	}
	return nil
}

func (p *groupPatch) set(op, attr string, value interface{}) {
	switch attr {
	case "displayname":
		if v, ok := value.(string); ok && v != "" {
			p.group.DisplayName = v
		}
	case "externalid":
		if v, ok := value.(string); ok && v != "" {
			p.group.ExternalID = v
		}
	case "members":
		ids := memberValues(value)
		p.named = append(p.named, ids...)
		if op == protocol.PatchOperationReplace {
			p.members = nil
		}
		for _, id := range ids {
			if !slices.Contains(p.members, id) {
				p.members = append(p.members, id)
			}
		}
	}
}

// memberFilterID reads the one member filter supported: `value eq "<id>"`.
func memberFilterID(expr filter.Expression) (string, bool) {
	e, ok := expr.(*filter.AttributeExpression)
	if !ok || e.Operator != filter.EQ || !strings.EqualFold(e.AttributePath.AttributeName, "value") || e.AttributePath.SubAttribute != nil || e.AttributePath.URIPrefix != nil {
		return "", false
	}
	id, ok := e.CompareValue.(string)
	return id, ok && id != ""
}

// replaceMembers moves groupID's members from old to next, touching only the difference.
func (h *groupResourceHandler) replaceMembers(r *http.Request, groupID string, old, next []string) error {
	for _, id := range old {
		if !slices.Contains(next, id) {
			if err := h.store.Groups().RemoveGroupMember(r.Context(), groupID, id); err != nil {
				return err
			}
		}
	}
	for _, id := range next {
		if !slices.Contains(old, id) {
			if err := h.store.Groups().AddGroupMember(r.Context(), groupID, id); err != nil {
				return err
			}
		}
	}
	return nil
}

func groupResource(group *store.Group) protocol.Resource {
	return protocol.Resource{ID: group.ID, ExternalID: optional.NewString(group.ExternalID), Attributes: protocol.ResourceAttributes{"displayName": group.DisplayName, "members": memberMaps(group.Members)}, Meta: protocol.Meta{Created: &group.CreatedAt, LastModified: &group.UpdatedAt}}
}
func memberMaps(ids []string) []interface{} {
	out := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		out = append(out, map[string]interface{}{"value": id})
	}
	return out
}
func memberValues(value interface{}) []string {
	var out []string
	for _, item := range interfaceSlice(value) {
		if m, ok := item.(map[string]interface{}); ok {
			if v, ok := m["value"].(string); ok && v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}
func primaryValue(value interface{}) string {
	for _, item := range interfaceSlice(value) {
		if m, ok := item.(map[string]interface{}); ok {
			if v, ok := m["value"].(string); ok {
				return v
			}
		}
		if v, ok := item.(string); ok {
			return v
		}
	}
	if v, ok := value.(string); ok {
		return v
	}
	return ""
}
func interfaceSlice(value interface{}) []interface{} {
	if values, ok := value.([]interface{}); ok {
		return values
	}
	return nil
}
func stringValue(attrs protocol.ResourceAttributes, key, fallback string) string {
	if value, ok := attrs[key].(string); ok && value != "" {
		return value
	}
	return fallback
}
func statusFromActive(attrs protocol.ResourceAttributes) string {
	if active, ok := attrs["active"].(bool); ok && !active {
		return "inactive"
	}
	return "active"
}
func scimStoreError(err error, id string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return protocolErrors.ScimErrorResourceNotFound(id)
	}
	if errors.Is(err, store.ErrAlreadyExists) {
		return protocolErrors.ScimErrorUniqueness
	}
	if errors.Is(err, store.ErrAccessChanged) { // RFC 7644 3.14: the resource changed; retry from a fresh read
		return protocolErrors.ScimError{Status: http.StatusPreconditionFailed, Detail: "The account changed since it was read; retry the request"}
	}
	return err
}
