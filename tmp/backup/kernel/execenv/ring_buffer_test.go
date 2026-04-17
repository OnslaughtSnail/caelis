package execenv

import (
	"testing"
)

func TestRingBuffer_BasicWriteRead(t *testing.T) {
	rb := NewRingBuffer(100)

	// Write some data
	n, err := rb.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != 5 {
		t.Fatalf("Expected 5 bytes written, got %d", n)
	}

	// Read all
	data := rb.ReadAll()
	if string(data) != "hello" {
		t.Fatalf("Expected 'hello', got %q", string(data))
	}
}

func TestRingBuffer_Wraparound(t *testing.T) {
	rb := NewRingBuffer(10)

	// Write more than capacity
	if _, err := rb.Write([]byte("12345")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if _, err := rb.Write([]byte("67890")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if _, err := rb.Write([]byte("abc")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Should keep the last 10 characters
	data := rb.ReadAll()
	if len(data) != 10 {
		t.Fatalf("Expected 10 bytes, got %d", len(data))
	}
	// The exact content depends on wraparound behavior
	t.Logf("Data after wraparound: %q", string(data))
}

func TestRingBuffer_ReadNewSince(t *testing.T) {
	rb := NewRingBuffer(100)

	if _, err := rb.Write([]byte("first")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	marker := rb.TotalWritten()

	if _, err := rb.Write([]byte("second")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	newData, newMarker := rb.ReadNewSince(marker)
	if string(newData) != "second" {
		t.Fatalf("Expected 'second', got %q", string(newData))
	}
	if newMarker != rb.TotalWritten() {
		t.Fatalf("Expected marker %d, got %d", rb.TotalWritten(), newMarker)
	}
}

func TestRingBuffer_LargeWrite(t *testing.T) {
	rb := NewRingBuffer(10)

	// Write data larger than capacity
	largeData := []byte("this is a very long string that exceeds the buffer capacity")
	if _, err := rb.Write(largeData); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Should only keep the last 10 bytes
	data := rb.ReadAll()
	if len(data) != 10 {
		t.Fatalf("Expected 10 bytes, got %d", len(data))
	}
	expected := largeData[len(largeData)-10:]
	if string(data) != string(expected) {
		t.Fatalf("Expected %q, got %q", string(expected), string(data))
	}
	if got := rb.DroppedBytes(); got != int64(len(largeData)-10) {
		t.Fatalf("expected dropped=%d, got %d", len(largeData)-10, got)
	}
	if got := rb.EarliestMarker(); got != int64(len(largeData)-10) {
		t.Fatalf("expected earliest marker=%d, got %d", len(largeData)-10, got)
	}
}

func TestRingBuffer_DroppedBytesAcrossWraparound(t *testing.T) {
	rb := NewRingBuffer(5)
	if _, err := rb.Write([]byte("abcde")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if _, err := rb.Write([]byte("fg")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if got := string(rb.ReadAll()); got != "cdefg" {
		t.Fatalf("expected retained tail, got %q", got)
	}
	if got := rb.DroppedBytes(); got != 2 {
		t.Fatalf("expected dropped=2, got %d", got)
	}
	if got := rb.EarliestMarker(); got != 2 {
		t.Fatalf("expected earliest marker=2, got %d", got)
	}
}
