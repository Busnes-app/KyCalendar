package config

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// leafPaths lists every field of t, descending into nested structs.
func leafPaths(t reflect.Type, prefix string) []string {
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		path := prefix + f.Name
		if f.Type.Kind() == reflect.Struct && f.Type != reflect.TypeOf(time.Time{}) {
			out = append(out, leafPaths(f.Type, path+".")...)
			continue
		}
		out = append(out, path)
	}
	return out
}

// Every Config field is classified shared or per host, so a new setting cannot slip past the
// replica check unnoticed; a listed field that no longer exists fails too.
func TestEveryConfigFieldIsClassified(t *testing.T) {
	fields := leafPaths(reflect.TypeOf(Config{}), "")
	for _, f := range fields {
		if _, ok := replicaFields[f]; !ok {
			t.Errorf("config field %s is not classified in replicaFields", f)
		}
	}
	for f := range replicaFields {
		if !slices.Contains(fields, f) {
			t.Errorf("replicaFields lists %s, which Config no longer has", f)
		}
	}
}

func replicaConfig() *Config {
	return &Config{Security: SecurityConfig{EncryptionKey: []byte(strings.Repeat("k", 32)), SessionSecret: "secret"}}
}

// Values are keyed: the fingerprint holds no value, and a different key changes every entry.
func TestReplicaFingerprintIsKeyed(t *testing.T) {
	c := replicaConfig()
	c.Server.AppURL = "https://cal.example.com"
	fp := ReplicaFingerprint(c)
	for name, v := range fp {
		if strings.Contains(v, "cal.example.com") || strings.Contains(v, "secret") || len(v) != 64 {
			t.Fatalf("%s carries a value: %q", name, v)
		}
	}
	other := replicaConfig()
	other.Server.AppURL = c.Server.AppURL
	other.Security.EncryptionKey = []byte(strings.Repeat("j", 32))
	if got := DiffFingerprint(fp, ReplicaFingerprint(other)); !slices.Equal(got, []string{EncryptionKeyField}) {
		t.Fatalf("a different key must be reported alone, got %v", got)
	}
}

// A name only the newer build knows is merged, not refused; a removed one is ignored.
func TestFingerprintMergesNewNames(t *testing.T) {
	cur := ReplicaFingerprint(replicaConfig())
	stored := map[string]string{}
	for k, v := range cur {
		if k != "KY_APP_URL" {
			stored[k] = v
		}
	}
	stored["KY_RETIRED"] = "x"
	if d := DiffFingerprint(stored, cur); len(d) != 0 {
		t.Fatalf("diff %v", d)
	}
	if !MergeFingerprint(stored, cur) || stored["KY_APP_URL"] != cur["KY_APP_URL"] || MergeFingerprint(stored, cur) {
		t.Fatal("merge did not add the new name once")
	}
}

// fieldByPath is the Config field at a dotted path.
func fieldByPath(c *Config, path string) reflect.Value {
	v := reflect.ValueOf(c).Elem()
	for _, name := range strings.Split(path, ".") {
		v = v.FieldByName(name)
	}
	return v
}

// mutate changes v to another value of its type.
func mutate(t *testing.T, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		v.SetString(v.String() + "-changed")
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.Int, reflect.Int64:
		v.SetInt(v.Int() + 1)
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			v.SetBytes(append([]byte("changed"), v.Bytes()...))
			return
		}
		v.Set(reflect.Append(v, reflect.Zero(v.Type().Elem())))
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
	default:
		t.Fatalf("cannot mutate %s", v.Kind())
	}
}

// A second replica differing in any shared setting is refused, the setting named and its
// value never shown; one differing only in per-host settings is not (spec AC14).
func TestEverySharedFieldIsCompared(t *testing.T) {
	base := replicaConfig()
	want := ReplicaFingerprint(base)
	for path, f := range replicaFields {
		if path == "Security.EncryptionKey" {
			continue // reported alone, TestReplicaFingerprintIsKeyed
		}
		t.Run(path, func(t *testing.T) {
			c := replicaConfig()
			mutate(t, fieldByPath(c, path))
			diff := DiffFingerprint(want, ReplicaFingerprint(c))
			if f.value == nil {
				if len(diff) != 0 {
					t.Fatalf("per-host %s differs as %v", path, diff)
				}
				return
			}
			if !slices.Equal(diff, []string{f.env}) {
				t.Fatalf("shared %s: diff %v, want [%s]", path, diff, f.env)
			}
		})
	}
}
