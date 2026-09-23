package store

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReadDumpVersion(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"версия первым ключом", `{"version":1,"monitors":[]}`, 1},
		{"версия после массивов", `{"monitors":[{"id":1}],"checks":[{"id":9}],"version":1}`, 1},
		{"версии нет", `{"monitors":[]}`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readDumpVersion(strings.NewReader(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestReadDumpVersionRejectsGarbage(t *testing.T) {
	for _, in := range []string{``, `[]`, `{`, `"строка"`, `{"version":`} {
		if _, err := readDumpVersion(strings.NewReader(in)); err == nil {
			t.Fatalf("expected error for %q", in)
		}
	}
}

func TestWalkDumpObjectSkipsUnknownKeys(t *testing.T) {
	// checks должен быть промотан без разбора: в первом проходе импорта
	// читаются только мониторы.
	in := `{
		"version": 1,
		"exported_at": "2026-09-17T12:00:00Z",
		"checks": [{"id": 1, "nested": {"a": [1, 2, {"b": null}]}}, {"id": 2}],
		"monitors": [{"id": 7, "name": "API"}, {"id": 8, "name": "Web"}],
		"unknown": {"deep": [[[1]]]}
	}`

	var names []string
	err := walkDumpObject(strings.NewReader(in), map[string]dumpHandler{
		"monitors": func(dec *json.Decoder) error {
			return eachArrayElement(dec, func() error {
				var m struct {
					Name string `json:"name"`
				}
				if err := dec.Decode(&m); err != nil {
					return err
				}
				names = append(names, m.Name)
				return nil
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "API,Web" {
		t.Fatalf("unexpected monitors: %#v", names)
	}
}

func TestWalkDumpObjectPropagatesHandlerError(t *testing.T) {
	in := `{"monitors":[{"id":1}]}`
	err := walkDumpObject(strings.NewReader(in), map[string]dumpHandler{
		"monitors": func(dec *json.Decoder) error {
			return eachArrayElement(dec, func() error {
				var raw json.RawMessage
				if err := dec.Decode(&raw); err != nil {
					return err
				}
				return errInvalidDump
			})
		},
	})
	if err == nil {
		t.Fatal("expected handler error to propagate")
	}
}

func TestWalkDumpObjectRejectsNonObject(t *testing.T) {
	if err := walkDumpObject(strings.NewReader(`[1,2,3]`), nil); err == nil {
		t.Fatal("expected error for top-level array")
	}
}

func TestEachArrayElementRequiresArray(t *testing.T) {
	dec := json.NewDecoder(strings.NewReader(`{"a":1}`))
	if err := eachArrayElement(dec, func() error { return nil }); err == nil {
		t.Fatal("expected error when value is not an array")
	}
}
