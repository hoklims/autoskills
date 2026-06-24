package cache

import "testing"

func TestLRUEvictsLeastRecentlyUsed(t *testing.T) {
	c := New[string, int](2)
	c.Add("a", 1)
	c.Add("b", 2)
	if got, ok := c.Get("a"); !ok || got != 1 { // touch a → b is now LRU
		t.Fatalf("Get(a) = %d,%v; want 1,true", got, ok)
	}
	c.Add("c", 3) // evicts b
	if c.Contains("b") {
		t.Errorf("b should have been evicted")
	}
	if !c.Contains("a") || !c.Contains("c") {
		t.Errorf("a and c should remain")
	}
	if c.Len() != 2 {
		t.Errorf("Len = %d; want 2", c.Len())
	}
}

func TestLRUUpdateDoesNotGrow(t *testing.T) {
	c := New[string, int](2)
	c.Add("a", 1)
	c.Add("a", 9) // update, not insert
	if got, _ := c.Get("a"); got != 9 {
		t.Errorf("Get(a) = %d; want 9", got)
	}
	if c.Len() != 1 {
		t.Errorf("Len = %d; want 1", c.Len())
	}
}

func TestLRUMissOnEmpty(t *testing.T) {
	c := New[string, bool](4)
	if _, ok := c.Get("nope"); ok {
		t.Errorf("Get on empty cache should miss")
	}
}

func TestLRUCapacityFloor(t *testing.T) {
	c := New[int, int](0) // coerced to 1
	c.Add(1, 1)
	c.Add(2, 2)
	if c.Len() != 1 {
		t.Errorf("Len = %d; want 1 (capacity floored to 1)", c.Len())
	}
	if c.Contains(1) {
		t.Errorf("oldest entry should have been evicted at capacity 1")
	}
}
