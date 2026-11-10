package web

import (
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// imageJob is one tool item's images waiting to be stored.
type imageJob struct {
	s               *webSession
	gen             uint64
	agentID, itemID string
	images          []agentapi.Image
}

// maxImageJobs bounds the images waiting to be stored. Past it, a tool
// item's images are left out with a note, as an over-cap image would be.
const maxImageJobs = 256

func (m *Manager) queueImages(job imageJob) {
	m.imageWG.Add(1)
	select {
	case m.imageJobs <- job:
	default:
		m.imageWG.Done()
		log.Warn("tool images dropped: too many waiting to be stored", "session", job.s.id, "count", len(job.images))
		m.applyImages(job, nil, imagesNote(len(job.images), "too many images arrived at once"))
	}
}

// imageLoop stores queued tool images one job at a time until the manager
// stops; jobs still queued then are released unstored.
func (m *Manager) imageLoop() {
	for {
		select {
		case <-m.ctx.Done():
			for {
				select {
				case <-m.imageJobs:
					m.imageWG.Done()
				default:
					return
				}
			}
		case job := <-m.imageJobs:
			m.storeImages(job)
		}
	}
}

// storeImages stores one job's images and puts them on their item, unless
// the conversation they came from is gone.
func (m *Manager) storeImages(job imageJob) {
	defer m.imageWG.Done() // also after a panic
	m.mu.Lock()
	stale := job.s.gen != job.gen || m.closed
	m.mu.Unlock()
	if stale {
		return
	}
	if m.storeImageHook != nil {
		m.storeImageHook()
	}
	it := agentapi.Item{Images: job.images}
	m.keepImages(job.s, &it)
	m.applyImages(job, it.Images, it.ImagesNote)
}

// applyImages puts stored images on the item they belong to and publishes
// it again, when that item and its conversation are still current.
func (m *Manager) applyImages(job imageJob, images []agentapi.Image, note string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := job.s
	if s.gen != job.gen || m.closed {
		return
	}
	i, ok := s.itemIdx[itemKey(job.agentID, job.itemID)]
	if !ok {
		return
	}
	it := s.items[i]
	it.Images, it.ImagesNote = images, note
	m.upsertItemLocked(s, clampItem(it, m.now()), true)
}
