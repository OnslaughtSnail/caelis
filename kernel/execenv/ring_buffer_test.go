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
	rb.Write([]byte("12345"))
	rb.Write([]byte("67890"))
	rb.Write([]byte("abc"))

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

	rb.Write([]byte("first"))
	marker := rb.TotalWritten()

	rb.Write([]byte("second"))
	
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
	rb.Write(largeData)

	// Should only keep the last 10 bytes
	data := rb.ReadAll()
	if len(data) != 10 {
		t.Fatalf("Expected 10 bytes, got %d", len(data))
	}
	expected := largeData[len(largeData)-10:]
	if string(data) != string(expected) {
		t.Fatalf("Expected %q, got %q", string(expected), string(data))
	}
}
