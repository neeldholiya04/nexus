package retrieval

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"go.uber.org/zap"

	"github.com/neeldholiya04/nexus/internal/memory"
)

type Config struct {
	SemanticWeight         float64
	RecencyWeight          float64
	CategoryWeight         float64
	ConfidenceWeight       float64
	RecencyHalfLifeDays    float64
	DefaultLimit           int
	MinConfidenceThreshold float64
}

type Pipeline struct {
	store MemoryStore
	embed Embedder
	cfg   Config
	log   *zap.Logger
}

func New(store MemoryStore, embed Embedder, cfg Config, log *zap.Logger) *Pipeline {
	return &Pipeline{store: store, embed: embed, cfg: cfg, log: log}
}

func (p *Pipeline) Retrieve(ctx context.Context, q memory.Query) ([]memory.RetrievalResult, error) {
	if q.Text == "" {
		return nil, fmt.Errorf("retrieval: query text cannot be empty")
	}

	limit := q.Limit
	if limit <= 0 {
		limit = p.cfg.DefaultLimit
	}
	minConf := q.MinConfidence
	if minConf <= 0 {
		minConf = p.cfg.MinConfidenceThreshold
	}

	queryEmbedding, err := p.embed.Embed(ctx, q.Text)
	if err != nil {
		p.log.Warn("retrieval: embedding failed, falling back to FTS-only", zap.Error(err))
		queryEmbedding = nil
	}

	ftsCandidates, err := p.store.FTSSearch(ctx, memory.FTSQuerySafe(q.Text), limit*5)
	if err != nil {
		p.log.Warn("retrieval: FTS search failed", zap.Error(err))
	}

	var vecCandidates []*memory.Memory
	if queryEmbedding != nil {
		vecCandidates, err = p.store.GetAllWithEmbeddings(ctx)
		if err != nil {
			p.log.Warn("retrieval: GetAllWithEmbeddings failed", zap.Error(err))
		}
	}

	candidateMap := make(map[string]*memory.Memory)
	for _, m := range ftsCandidates {
		candidateMap[m.ID] = m
	}
	for _, m := range vecCandidates {
		candidateMap[m.ID] = m
	}
	if len(candidateMap) == 0 {
		p.log.Debug("retrieval: no candidates found")
		return nil, nil
	}

	now := time.Now()
	var results []memory.RetrievalResult

	for _, m := range candidateMap {
		effectiveConf := m.EffectiveConfidence(now)

		if effectiveConf < minConf {
			continue
		}
		if len(q.Categories) > 0 && !containsCategory(q.Categories, m.Category) {
			continue
		}
		if !q.IncludeExpired && !m.IsValid() {
			continue
		}

		r := memory.RetrievalResult{Memory: m}
		if queryEmbedding != nil && m.HasEmbedding() {
			r.SemanticScore = memory.CosineSimilarity(queryEmbedding, m.Embedding)
		}
		r.RecencyScore = recencyScore(m.UpdatedAt, now, p.cfg.RecencyHalfLifeDays)
		r.CategoryScore = categoryScore(m.Category, q.Categories)
		r.ConfidenceScore = effectiveConf
		r.FinalScore = p.cfg.SemanticWeight*r.SemanticScore +
			p.cfg.RecencyWeight*r.RecencyScore +
			p.cfg.CategoryWeight*r.CategoryScore +
			p.cfg.ConfidenceWeight*r.ConfidenceScore

		results = append(results, r)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].FinalScore > results[j].FinalScore
	})
	if len(results) > limit {
		results = results[:limit]
	}

	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.Memory.ID
	}
	if err := p.store.RecordAccess(ctx, ids); err != nil {
		p.log.Warn("retrieval: RecordAccess failed", zap.Error(err))
	}

	p.log.Debug("retrieval: complete",
		zap.Int("candidates", len(candidateMap)),
		zap.Int("results", len(results)),
	)
	return results, nil
}

func recencyScore(updatedAt, now time.Time, halfLife float64) float64 {
	if halfLife <= 0 {
		halfLife = 30
	}
	age := now.Sub(updatedAt).Hours() / 24.0
	if age < 0 {
		age = 0
	}
	return math.Pow(2, -age/halfLife)
}

func categoryScore(cat memory.Category, filter []memory.Category) float64 {
	if len(filter) > 0 {
		if containsCategory(filter, cat) {
			return 1.0
		}
		return 0.3
	}
	weights := map[memory.Category]float64{
		memory.CategoryFact:        0.85,
		memory.CategoryProject:     0.85,
		memory.CategoryWorkflow:    0.75,
		memory.CategoryCodingStyle: 0.70,
		memory.CategoryPreference:  0.65,
		memory.CategoryInferred:    0.50,
	}
	if w, ok := weights[cat]; ok {
		return w
	}
	return 0.5
}

func containsCategory(cats []memory.Category, target memory.Category) bool {
	for _, c := range cats {
		if c == target {
			return true
		}
	}
	return false
}
