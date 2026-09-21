package udm

import (
	"bytes"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSnapshotIncludesUpdateTime(t *testing.T) {
	u := New()
	at := time.Date(2026, 9, 21, 1, 2, 3, 0, time.UTC)
	u.UpdateAt("Speed", 12.0, at)
	snapshot := u.Snapshot()
	if snapshot["Speed"].Value != 12.0 || !snapshot["Speed"].UpdatedAt.Equal(at) {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	delete(snapshot, "Speed")
	if _, ok := u.Snapshot()["Speed"]; !ok {
		t.Fatal("snapshot mutated source data")
	}
}

func TestNew(t *testing.T) {
	udm := New()
	if udm == nil {
		t.Fatal("New() returned nil")
	}
	if udm.data == nil {
		t.Fatal("data map is nil")
	}
}

func TestUpdateAndGet(t *testing.T) {
	udm := New()
	udm.Update("temperature", 25.5)

	val, ok := udm.Get("temperature")
	if !ok {
		t.Fatal("key not found")
	}
	if val != 25.5 {
		t.Errorf("value = %v, want 25.5", val)
	}
}

func TestGet_Missing(t *testing.T) {
	udm := New()
	val, ok := udm.Get("nonexistent")
	if ok {
		t.Error("expected missing key")
	}
	if val != nil {
		t.Errorf("expected nil value, got %v", val)
	}
}

func TestUpdate_Overwrite(t *testing.T) {
	udm := New()
	udm.Update("a", 1)
	udm.Update("a", 2)

	val, _ := udm.Get("a")
	if val != 2 {
		t.Errorf("value = %v, want 2", val)
	}
}

func TestGetAll(t *testing.T) {
	udm := New()
	udm.Update("a", 1)
	udm.Update("b", true)
	udm.Update("c", 3.14)

	snapshot := udm.GetAll()
	if len(snapshot) != 3 {
		t.Fatalf("snapshot size = %d, want 3", len(snapshot))
	}
	if snapshot["a"] != 1 {
		t.Errorf("a = %v", snapshot["a"])
	}
	if snapshot["b"] != true {
		t.Errorf("b = %v", snapshot["b"])
	}
	if snapshot["c"] != 3.14 {
		t.Errorf("c = %v", snapshot["c"])
	}

	// Verify snapshot is independent
	udm.Update("a", 999)
	if snapshot["a"] != 1 {
		t.Errorf("snapshot should not be affected by subsequent updates")
	}
}

func TestConcurrentAccess(t *testing.T) {
	udm := New()
	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			udm.Update("counter", n)
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			udm.Get("counter")
		}()
	}

	// Concurrent GetAll
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			udm.GetAll()
		}()
	}

	wg.Wait()

	// After all writes, value should be set
	val, ok := udm.Get("counter")
	if !ok {
		t.Error("counter should exist after concurrent writes")
	}
	_ = val
}

func TestLogAll_Empty(t *testing.T) {
	udm := New()
	// Should not panic on empty data
	udm.LogAll()
}

func TestLogAll_WithData(t *testing.T) {
	udm := New()
	udm.Update("x", 42)
	// Should not panic
	udm.LogAll()
}

func TestLogAllWithDevice_IncludesDeviceName(t *testing.T) {
	udm := New()
	udm.Update("Tag_352", false)

	var buf bytes.Buffer
	originalOutput := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(originalOutput)
		log.SetFlags(originalFlags)
	}()

	udm.LogAllWithDevice("plc-1")

	got := buf.String()
	if !strings.Contains(got, "[DATA] plc-1.Tag_352 = false") {
		t.Fatalf("log output = %q, want device-qualified tag", got)
	}
}
