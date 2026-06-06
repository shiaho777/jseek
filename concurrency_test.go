package jseek

import (
	"sync"
	"testing"
)

// TestDocumentConcurrentReads backs the documented guarantee that a Document's
// read methods are safe for concurrent use. Run with -race to detect any hidden
// write on the read path.
func TestDocumentConcurrentReads(t *testing.T) {
	d := IndexTape(sample)
	const goroutines = 16
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				if s, err := d.GetString("person", "name", "fullName"); err != nil || s != "Leonid Bugaev" {
					t.Errorf("concurrent read got %q %v", s, err)
					return
				}
				_, _ = d.GetInt("person", "github", "followers")
				_ = d.Exists("company", "name")
			}
		}()
	}
	wg.Wait()
}

// TestStatelessConcurrentReads backs the same guarantee for the package-level
// functions over a shared input slice.
func TestStatelessConcurrentReads(t *testing.T) {
	const goroutines = 16
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				_, _ = GetString(sample, "company", "name")
				_, _ = GetInt(sample, "company", "size")
			}
		}()
	}
	wg.Wait()
}
