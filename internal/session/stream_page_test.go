package session

import (
	"reflect"
	"testing"
)

func TestPageLinesBefore(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e"}
	tests := []struct {
		name      string
		before    int
		limit     int
		want      []string
		wantStart int
	}{
		{"middle page", 4, 2, []string{"c", "d"}, 2},
		{"before=0 returns empty", 0, 2, []string{}, 0},
		{"before>total is clamped", 10, 2, []string{"d", "e"}, 3},
		{"limit bigger than available", 3, 100, []string{"a", "b", "c"}, 0},
		{"limit exactly reaches start", 2, 2, []string{"a", "b"}, 0},
		{"limit<=0 returns all before cursor", 4, 0, []string{"a", "b", "c", "d"}, 0},
		{"negative before", -1, 2, []string{}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, start := PageLinesBefore(lines, tt.before, tt.limit)
			if len(got) == 0 && len(tt.want) == 0 {
				got = []string{}
			}
			if !reflect.DeepEqual(got, tt.want) || start != tt.wantStart {
				t.Errorf("PageLinesBefore(before=%d, limit=%d) = %v, %d; want %v, %d",
					tt.before, tt.limit, got, start, tt.want, tt.wantStart)
			}
		})
	}

	got, start := PageLinesBefore(nil, 5, 10)
	if len(got) != 0 || start != 0 {
		t.Errorf("empty input: got %v, %d", got, start)
	}
}

func TestPageLinesBefore_OverFilteredFileLines(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	sessionID := "test-before-fallback"
	writeStreamFixture(t, sessionID, streamFixtureWithTransients)

	m := &Manager{}
	all, err := m.readStreamFile(sessionID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 3 non-transient lines: init, assistant, result. Page before index 2.
	page, start := PageLinesBefore(all, 2, 1)
	if start != 1 || len(page) != 1 || page[0] != all[1] {
		t.Fatalf("unexpected page %v start=%d", page, start)
	}
}
