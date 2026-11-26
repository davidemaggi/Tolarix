package azuredevops

import (
	"context"
	"strings"
	"testing"
)

func TestPushCostCountsBase64(t *testing.T) {
	c := change{Type: "add", Path: "/a.png", Size: 3 << 20}
	if got := pushCost(c); got < 4<<20 {
		t.Fatalf("cost %d does not account for base64", got)
	}
	// The real limit leaves the request under Azure DevOps' 25 MiB.
	if maxPushBytes > 25<<20-1<<20 {
		t.Fatalf("maxPushBytes %d too close to the 25 MiB request limit", maxPushBytes)
	}
}

func TestSplitChangesByRequestSize(t *testing.T) {
	mb := int64(1 << 20)
	var cs []change
	for range 10 {
		cs = append(cs, change{Type: "add", Path: "/f.bin", Size: 5 * mb})
	}
	batches := splitChanges(cs, maxPushBytes)
	if len(batches) < 3 {
		t.Fatalf("batches = %d", len(batches))
	}
	for i, b := range batches {
		var total int64
		for _, c := range b {
			total += pushCost(c)
		}
		if total > maxPushBytes {
			t.Fatalf("batch %d is %d bytes, over %d", i, total, maxPushBytes)
		}
	}
}

func TestPushRejectsFilesThatCannotFit(t *testing.T) {
	r := &Repo{} // never reaches the network
	_, err := r.push(context.Background(), "main", "", "msg", []change{
		{Type: "add", Path: "/wiki/.attachments/small.png", Size: 10, Content: func() ([]byte, error) { return nil, nil }},
		{Type: "add", Path: "/wiki/.attachments/video.mp4", Size: 30 << 20, Content: func() ([]byte, error) { return nil, nil }},
	})
	if err == nil || !strings.Contains(err.Error(), "video.mp4") || strings.Contains(err.Error(), "small.png") ||
		!strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("err = %v", err)
	}
}
