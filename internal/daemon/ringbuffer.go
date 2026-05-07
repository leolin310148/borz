package daemon

// RingBuffer is a fixed-capacity circular buffer. When full, oldest entries are silently evicted.
type RingBuffer[T any] struct {
	items    []T
	head     int // next write position
	count    int
	capacity int
}

// NewRingBuffer creates a ring buffer with the given capacity.
func NewRingBuffer[T any](capacity int) *RingBuffer[T] {
	return &RingBuffer[T]{
		items:    make([]T, capacity),
		capacity: capacity,
	}
}

// Size returns the number of stored elements.
func (rb *RingBuffer[T]) Size() int {
	return rb.count
}

// Push appends an element, evicting the oldest if at capacity.
func (rb *RingBuffer[T]) Push(item T) {
	rb.items[rb.head] = item
	rb.head = (rb.head + 1) % rb.capacity
	if rb.count < rb.capacity {
		rb.count++
	}
}

// Find returns the first stored element matching match.
func (rb *RingBuffer[T]) Find(match func(*T) bool) *T {
	if rb.count == 0 {
		return nil
	}
	start := 0
	if rb.count == rb.capacity {
		start = rb.head
	}
	for i := 0; i < rb.count; i++ {
		item := &rb.items[(start+i)%rb.capacity]
		if match(item) {
			return item
		}
	}
	return nil
}

// ToSlice returns all stored elements in insertion order (oldest first).
func (rb *RingBuffer[T]) ToSlice() []T {
	if rb.count == 0 {
		return nil
	}
	result := make([]T, rb.count)
	start := 0
	if rb.count == rb.capacity {
		start = rb.head
	}
	for i := 0; i < rb.count; i++ {
		result[i] = rb.items[(start+i)%rb.capacity]
	}
	return result
}

// Clear removes all elements.
func (rb *RingBuffer[T]) Clear() {
	var zero T
	for i := range rb.items {
		rb.items[i] = zero
	}
	rb.head = 0
	rb.count = 0
}
