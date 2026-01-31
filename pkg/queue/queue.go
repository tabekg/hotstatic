package queue

import (
	"container/heap"
	"context"
	"sync"
	"time"
)

// Item represents a page to rebuild in the queue.
type Item struct {
	// Path of the page to rebuild
	Path string

	// Priority (higher = more urgent)
	Priority int

	// Timestamp when added to queue
	AddedAt time.Time

	// TriggerKey that caused this rebuild
	TriggerKey string

	// index for heap implementation
	index int
}

// Queue is a priority queue for page rebuilds.
type Queue struct {
	items    priorityQueue
	itemSet  map[string]*Item // For deduplication
	mu       sync.Mutex
	notEmpty chan struct{}
	closed   bool
}

// New creates a new priority queue.
func New() *Queue {
	q := &Queue{
		items:    make(priorityQueue, 0),
		itemSet:  make(map[string]*Item),
		notEmpty: make(chan struct{}, 1),
	}
	heap.Init(&q.items)
	return q
}

// Push adds an item to the queue.
// If the path already exists, updates priority if higher.
func (q *Queue) Push(item Item) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return
	}

	if item.AddedAt.IsZero() {
		item.AddedAt = time.Now()
	}

	// Check for existing item
	if existing, ok := q.itemSet[item.Path]; ok {
		// Update priority if higher
		if item.Priority > existing.Priority {
			existing.Priority = item.Priority
			existing.TriggerKey = item.TriggerKey
			heap.Fix(&q.items, existing.index)
		}
		return
	}

	// Add new item
	newItem := &Item{
		Path:       item.Path,
		Priority:   item.Priority,
		AddedAt:    item.AddedAt,
		TriggerKey: item.TriggerKey,
	}
	heap.Push(&q.items, newItem)
	q.itemSet[item.Path] = newItem

	// Signal not empty
	select {
	case q.notEmpty <- struct{}{}:
	default:
	}
}

// Pop removes and returns the highest priority item.
// Returns nil if queue is empty.
func (q *Queue) Pop() *Item {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.items.Len() == 0 {
		return nil
	}

	item := heap.Pop(&q.items).(*Item)
	delete(q.itemSet, item.Path)
	return item
}

// PopWait blocks until an item is available or context is cancelled.
func (q *Queue) PopWait(ctx context.Context) *Item {
	for {
		if item := q.Pop(); item != nil {
			return item
		}

		select {
		case <-ctx.Done():
			return nil
		case <-q.notEmpty:
			// Try again
		}
	}
}

// Len returns the number of items in the queue.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.items.Len()
}

// Contains checks if a path is in the queue.
func (q *Queue) Contains(path string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	_, ok := q.itemSet[path]
	return ok
}

// Remove removes an item by path.
func (q *Queue) Remove(path string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	item, ok := q.itemSet[path]
	if !ok {
		return false
	}

	heap.Remove(&q.items, item.index)
	delete(q.itemSet, path)
	return true
}

// Clear removes all items from the queue.
func (q *Queue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.items = make(priorityQueue, 0)
	q.itemSet = make(map[string]*Item)
	heap.Init(&q.items)
}

// Close closes the queue.
func (q *Queue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	close(q.notEmpty)
}

// Items returns a copy of all items (for inspection).
func (q *Queue) Items() []Item {
	q.mu.Lock()
	defer q.mu.Unlock()

	result := make([]Item, len(q.items))
	for i, item := range q.items {
		result[i] = *item
	}
	return result
}

// priorityQueue implements heap.Interface.
type priorityQueue []*Item

func (pq priorityQueue) Len() int { return len(pq) }

func (pq priorityQueue) Less(i, j int) bool {
	// Higher priority first
	if pq[i].Priority != pq[j].Priority {
		return pq[i].Priority > pq[j].Priority
	}
	// Earlier timestamp first (FIFO for same priority)
	return pq[i].AddedAt.Before(pq[j].AddedAt)
}

func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *priorityQueue) Push(x any) {
	n := len(*pq)
	item := x.(*Item)
	item.index = n
	*pq = append(*pq, item)
}

func (pq *priorityQueue) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil // avoid memory leak
	item.index = -1
	*pq = old[0 : n-1]
	return item
}
