package web

import (
	"context"
	"fmt"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// holdImageStore makes the image goroutine stop at the first job it stores
// until release is closed; entered is signalled when it does.
func holdImageStore(m *Manager) (entered, release chan struct{}) {
	entered, release = make(chan struct{}, 1), make(chan struct{})
	m.storeImageHook = func() {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	}
	return entered, release
}

func itemByID(d SessionDetail, id string) (agentapi.Item, bool) {
	for _, it := range d.Items {
		if it.ID == id {
			return it, true
		}
	}
	return agentapi.Item{}, false
}

// Past maxImageJobs waiting jobs, a tool item's images are left out with a
// note. An item dropped from the retained transcript while its images were
// stored does not come back with them.
func TestToolImagesPastTheWaitingCapAreLeftOutWithANote(t *testing.T) {
	m, _, sum, conv, _ := uploadTask(t, "docs")
	img := pngBytes(t)
	entered, release := holdImageStore(m)
	conv.EmitItem(toolItem("t0", "", toolImage("shot.png", img)))
	within(t, "the first image job", entered)
	for i := 1; i <= maxImageJobs; i++ {
		conv.EmitItem(toolItem(fmt.Sprintf("t%d", i), "", toolImage("shot.png", img)))
	}
	conv.EmitItem(toolItem("over", "", toolImage("shot.png", img)))
	over, ok := itemByID(detail(t, m, sum.ID), "over")
	if !ok || len(over.Images) != 0 || over.ImagesNote != "1 image not kept: too many images arrived at once" {
		t.Fatalf("item past the cap = %+v", over)
	}
	// One trim's worth of later items pushes t0, still being stored, out of
	// the transcript; the last queued items stay.
	for i := range maxItems + 1 - (maxImageJobs + 2) {
		conv.EmitItem(agentapi.Item{ID: fmt.Sprintf("a%d", i), Kind: agentapi.ItemAssistant, Text: "x"})
	}
	close(release)
	m.imageWG.Wait()
	d := detail(t, m, sum.ID)
	if _, ok := itemByID(d, "t0"); ok || !d.HistoryTruncated {
		t.Fatalf("t0 came back after it was dropped (truncated %v)", d.HistoryTruncated)
	}
	if last, ok := itemByID(d, fmt.Sprintf("t%d", maxImageJobs)); !ok || len(last.Images) != 1 {
		t.Fatalf("last queued item = %+v", last)
	}
}

// Image jobs still waiting when the service stops are released unstored,
// and the job being stored then is not put on its item.
func TestToolImagesWaitingAtShutdownAreReleased(t *testing.T) {
	m, _, sum, conv, _ := uploadTask(t, "docs")
	img := pngBytes(t)
	entered, release := holdImageStore(m)
	for i := range 64 {
		conv.EmitItem(toolItem(fmt.Sprintf("t%d", i), "", toolImage("shot.png", img)))
	}
	within(t, "the first image job", entered)
	stopped := make(chan error, 1)
	go func() { stopped <- m.Shutdown(context.Background()) }()
	waitUntil(t, "shutdown to cancel work", func() bool { return m.ctx.Err() != nil })
	close(release)
	if err := <-stopped; err != nil {
		t.Fatalf("shutdown = %v", err)
	}
	m.imageWG.Wait()
	for _, it := range detail(t, m, sum.ID).Items {
		if len(it.Images) != 0 || it.ImagesNote != "" {
			t.Fatalf("image stored during shutdown: %+v", it)
		}
	}
}
