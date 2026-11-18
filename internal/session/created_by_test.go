package session

import "testing"

// CreatedBySessionID is persisted by Create and read back by Get / List (#805).
func TestCreate_RecordsCreatedBySessionID(t *testing.T) {
	m, rec := autoStopTestManager(t)
	s, err := m.Create("child", t.TempDir(), "do it", false, CreateOpts{CreatedBySessionID: "coord-1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rec.waitSpawn()
	got, err := m.Get(s.ID)
	if err != nil || got.CreatedBySessionID != "coord-1" {
		t.Fatalf("Get: %+v, %v", got, err)
	}
	list, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range list {
		if x.ID == s.ID {
			found = true
			if x.CreatedBySessionID != "coord-1" {
				t.Errorf("List createdBy = %q", x.CreatedBySessionID)
			}
		}
	}
	if !found {
		t.Fatal("session missing from List")
	}
}
