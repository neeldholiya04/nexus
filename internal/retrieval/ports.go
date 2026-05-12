package retrieval

import (
	"context"

	"github.com/neeldholiya04/nexus/internal/memory"
)

type MemoryStore interface {
	FTSSearch(ctx context.Context, query string, limit int) ([]*memory.Memory, error)
	GetAllWithEmbeddings(ctx context.Context) ([]*memory.Memory, error)
	RecordAccess(ctx context.Context, ids []string) error
}

type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}
