package api

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Busnes-app/ky-primitives/suitedir"
)

// person is one organisation member from KyIdentity's people search (suite contract A2/D).
type person struct {
	Sub   string `json:"sub"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// searchPeople asks KyIdentity's suite directory for people matching q, as sub. A variable so
// tests can stand in for KyIdentity.
var searchPeople = func(ctx context.Context, dir *suitedir.Client, sub, q string, limit int) ([]person, error) {
	found, err := dir.People(ctx, sub, q, limit)
	people := make([]person, 0, len(found))
	for _, p := range found {
		people = append(people, person{Sub: p.Sub, Name: p.Name, Email: p.Email})
	}
	return people, err
}

// suitePeopleTTL is how long one user's answer to one query is reused.
var suitePeopleTTL = time.Minute

type suitePeopleCache struct {
	mu    sync.Mutex
	byKey map[string]suitePeopleEntry
}

type suitePeopleEntry struct {
	people []person
	until  time.Time
}

func (c *suitePeopleCache) get(key string) ([]person, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byKey[key]
	if !ok || time.Now().After(e.until) {
		return nil, false
	}
	return e.people, true
}

func (c *suitePeopleCache) put(key string, people []person) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.byKey == nil {
		c.byKey = map[string]suitePeopleEntry{}
	}
	for k, e := range c.byKey {
		if now.After(e.until) {
			delete(c.byKey, k)
		}
	}
	c.byKey[key] = suitePeopleEntry{people: people, until: now.Add(suitePeopleTTL)}
}

// handleSuitePeople is the attendee field's autocomplete: GET /api/suite/people?q=, people in
// the organisation who may open KyCalendar. Every failure is an empty list.
func (s *Server) handleSuitePeople(w http.ResponseWriter, r *http.Request) {
	u := sessionUser(r.Context())
	none := map[string][]person{"people": {}}
	q := r.URL.Query().Get("q")
	if n := utf8.RuneCountInString(q); n < 2 || n > 64 || !utf8.ValidString(q) {
		s.writeJSON(w, http.StatusOK, none)
		return
	}
	if !s.allowAccountAttempt("people:"+u.ID, 60, time.Minute) {
		s.writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "Too many searches; wait a minute", "code": "rate_limited"})
		return
	}
	p := s.signin.Load()
	if p == nil || !s.suiteAccount(u, p) {
		s.writeJSON(w, http.StatusOK, none)
		return
	}
	key := p.ID + "\x00" + u.ID + "\x00" + q
	if people, ok := s.suitePeople.get(key); ok {
		s.writeJSON(w, http.StatusOK, map[string][]person{"people": people})
		return
	}
	people, err := func() ([]person, error) {
		dir, err := p.SuiteDirectory()
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		return searchPeople(ctx, dir, u.SSOSubject, q, 10)
	}()
	if err != nil {
		log.Printf("suite: people search for %s: %v", u.ID, err) // never the query
		people = []person{}
	}
	if people == nil {
		people = []person{}
	}
	if r.Context().Err() == nil {
		s.suitePeople.put(key, people)
	}
	s.writeJSON(w, http.StatusOK, map[string][]person{"people": people})
}
