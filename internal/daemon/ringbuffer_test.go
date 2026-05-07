package daemon

import (
	"reflect"
	"testing"
)

func TestRingBuffer_EmptyToSliceReturnsNil(t *testing.T) {
	rb := NewRingBuffer[int](3)
	if rb.Size() != 0 {
		t.Errorf("Size() = %d, want 0", rb.Size())
	}
	if s := rb.ToSlice(); s != nil {
		t.Errorf("ToSlice() empty = %v, want nil", s)
	}
}

func TestRingBuffer_PushUnderCapacity(t *testing.T) {
	rb := NewRingBuffer[int](3)
	rb.Push(1)
	rb.Push(2)
	if rb.Size() != 2 {
		t.Fatalf("Size() = %d, want 2", rb.Size())
	}
	if got := rb.ToSlice(); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Errorf("ToSlice() = %v, want [1 2]", got)
	}
}

func TestRingBuffer_ExactlyFull(t *testing.T) {
	rb := NewRingBuffer[int](3)
	rb.Push(1)
	rb.Push(2)
	rb.Push(3)
	if rb.Size() != 3 {
		t.Fatalf("Size() = %d, want 3", rb.Size())
	}
	if got := rb.ToSlice(); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Errorf("ToSlice() = %v, want [1 2 3]", got)
	}
}

func TestRingBuffer_WrapAroundEvictsOldest(t *testing.T) {
	rb := NewRingBuffer[int](3)
	for i := 1; i <= 5; i++ {
		rb.Push(i)
	}
	if rb.Size() != 3 {
		t.Fatalf("Size() = %d, want 3", rb.Size())
	}
	if got := rb.ToSlice(); !reflect.DeepEqual(got, []int{3, 4, 5}) {
		t.Errorf("ToSlice() after wrap = %v, want [3 4 5]", got)
	}
}

func TestRingBuffer_Find(t *testing.T) {
	rb := NewRingBuffer[int](3)
	for i := 1; i <= 5; i++ {
		rb.Push(i)
	}

	item := rb.Find(func(v *int) bool { return *v == 4 })
	if item == nil {
		t.Fatal("Find(4) returned nil")
	}
	*item = 40
	if got := rb.ToSlice(); !reflect.DeepEqual(got, []int{3, 40, 5}) {
		t.Errorf("ToSlice after Find mutation = %v, want [3 40 5]", got)
	}

	if item := rb.Find(func(v *int) bool { return *v == 2 }); item != nil {
		t.Errorf("Find(2) = %v, want nil for evicted item", *item)
	}
}

func TestRingBuffer_Clear(t *testing.T) {
	rb := NewRingBuffer[string](2)
	rb.Push("a")
	rb.Push("b")
	rb.Push("c") // evicts "a"
	rb.Clear()
	if rb.Size() != 0 {
		t.Errorf("Size after Clear = %d, want 0", rb.Size())
	}
	if s := rb.ToSlice(); s != nil {
		t.Errorf("ToSlice after Clear = %v, want nil", s)
	}
	// Usable after clear
	rb.Push("x")
	if got := rb.ToSlice(); !reflect.DeepEqual(got, []string{"x"}) {
		t.Errorf("ToSlice after reuse = %v, want [x]", got)
	}
}

func TestRingBuffer_ClearZeroesBackingArray(t *testing.T) {
	type big struct{ p *int }
	n := 7
	rb := NewRingBuffer[big](2)
	rb.Push(big{&n})
	rb.Push(big{&n})
	rb.Clear()
	// Inspect underlying items to ensure references were cleared so GC can reclaim them.
	for i, it := range rb.items {
		if it.p != nil {
			t.Errorf("items[%d].p = %v, want nil after Clear", i, it.p)
		}
	}
}

func TestRingBuffer_GenericType(t *testing.T) {
	rb := NewRingBuffer[string](2)
	rb.Push("a")
	rb.Push("b")
	rb.Push("c")
	if got := rb.ToSlice(); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Errorf("ToSlice = %v, want [b c]", got)
	}
}
