//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"go.uber.org/zap"

	"github.com/neeldholiya04/nexus/internal/app"
	nexusconfig "github.com/neeldholiya04/nexus/internal/config"
	"github.com/neeldholiya04/nexus/internal/embeddings/ollama"
	"github.com/neeldholiya04/nexus/internal/memory"
	"github.com/neeldholiya04/nexus/internal/retrieval"
	"github.com/neeldholiya04/nexus/internal/storage/sqlite"
)

type BenchmarkRun struct {
	Version    string           `json:"version"`
	Timestamp  time.Time        `json:"timestamp"`
	GitCommit  string           `json:"git_commit,omitempty"`
	Config     ScoringConfig    `json:"scoring_config"`
	Results    []QueryResult    `json:"query_results"`
	Aggregate  AggregateMetrics `json:"aggregate"`
	CorpusSize int              `json:"corpus_size"`
	QueryCount int              `json:"query_count"`
}

type ScoringConfig struct {
	SemanticWeight      float64 `json:"semantic_weight"`
	RecencyWeight       float64 `json:"recency_weight"`
	CategoryWeight      float64 `json:"category_weight"`
	ConfidenceWeight    float64 `json:"confidence_weight"`
	RecencyHalfLifeDays float64 `json:"recency_half_life_days"`
	EmbeddingModel      string  `json:"embedding_model"`
}

type QueryResult struct {
	Query        string  `json:"query"`
	PrecisionAtK float64 `json:"precision_at_k"`
	RecallAtK    float64 `json:"recall_at_k"`
	MRR          float64 `json:"mrr"`
	NDCGAtK      float64 `json:"ndcg_at_k"`
	K            int     `json:"k"`
}

type AggregateMetrics struct {
	MeanPrecisionAtK float64 `json:"mean_precision_at_k"`
	MeanRecallAtK    float64 `json:"mean_recall_at_k"`
	MeanMRR          float64 `json:"mean_mrr"`
	MeanNDCG         float64 `json:"mean_ndcg"`
}

type Delta struct {
	MRR  float64
	NDCG float64
	P    float64
}

func main() {
	compareVersion := flag.String("compare", "", "Version to compare against (e.g. v0.1.0)")
	outputDir := flag.String("output", "benchmarks/results", "Directory to write results")
	version := flag.String("version", "v0.1.0", "Version label for this run")
	live := flag.Bool("live", false, "Run against the configured live Nexus store instead of the pseudo corpus")
	liveQueries := flag.Int("live-queries", 10, "Max self-recall queries to derive from live memories")
	flag.Parse()

	var (
		run BenchmarkRun
		err error
	)
	if *live {
		run, err = executeLiveBenchmark(context.Background(), *version, *liveQueries)
	} else {
		run = executeBenchmark(*version)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "benchmark: %v\n", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		os.Exit(1)
	}

	outPath := filepath.Join(*outputDir, run.Version+".json")
	b, _ := json.MarshalIndent(run, "", "  ")
	if err := os.WriteFile(outPath, b, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write results: %v\n", err)
		os.Exit(1)
	}

	printReport(run)

	if *compareVersion != "" {
		prevPath := filepath.Join(*outputDir, *compareVersion+".json")
		prev, err := loadRun(prevPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "load previous run: %v\n", err)
		} else {
			printComparison(*prev, run)
		}
	}
}

func executeBenchmark(version string) BenchmarkRun {
	cfg := ScoringConfig{
		SemanticWeight:      0.45,
		RecencyWeight:       0.25,
		CategoryWeight:      0.20,
		ConfidenceWeight:    0.10,
		RecencyHalfLifeDays: 30,
		EmbeddingModel:      "pseudo-deterministic-v0",
	}

	corpus := buildCorpus()
	queries := buildQueries()
	now := time.Now()

	var queryResults []QueryResult
	var totalP, totalR, totalMRR, totalNDCG float64

	for _, q := range queries {
		queryVec := pseudoEmbed(q.embIdx, 768)
		var results []scoredMemory

		for _, m := range corpus {
			sem := cosineSimilarity(queryVec, m.embedding)
			rec := recencyScore(m.updatedAt, now, cfg.RecencyHalfLifeDays)
			cat := categoryScore(m.category, q.filterCats)
			conf := m.confidence

			score := cfg.SemanticWeight*sem +
				cfg.RecencyWeight*rec +
				cfg.CategoryWeight*cat +
				cfg.ConfidenceWeight*conf

			results = append(results, scoredMemory{id: m.id, score: score})
		}

		sort.Slice(results, func(i, j int) bool {
			return results[i].score > results[j].score
		})

		m := computeMetrics(results, q.relevant, q.k)
		totalP += m.PrecisionAtK
		totalR += m.RecallAtK
		totalMRR += m.MRR
		totalNDCG += m.NDCGAtK

		queryResults = append(queryResults, QueryResult{
			Query:        q.text,
			PrecisionAtK: m.PrecisionAtK,
			RecallAtK:    m.RecallAtK,
			MRR:          m.MRR,
			NDCGAtK:      m.NDCGAtK,
			K:            q.k,
		})
	}

	n := float64(len(queries))
	return BenchmarkRun{
		Version:    version,
		Timestamp:  now,
		Config:     cfg,
		Results:    queryResults,
		CorpusSize: len(corpus),
		QueryCount: len(queries),
		Aggregate: AggregateMetrics{
			MeanPrecisionAtK: totalP / n,
			MeanRecallAtK:    totalR / n,
			MeanMRR:          totalMRR / n,
			MeanNDCG:         totalNDCG / n,
		},
	}
}

func executeLiveBenchmark(ctx context.Context, version string, maxQueries int) (BenchmarkRun, error) {
	if maxQueries <= 0 {
		maxQueries = 10
	}
	_ = godotenv.Load(".env")

	cfg, err := nexusconfig.Load()
	if err != nil {
		return BenchmarkRun{}, fmt.Errorf("load config: %w", err)
	}

	log := zap.NewNop()
	db, err := sqlite.New(sqlite.Config{
		Path:          resolveBenchmarkDBPath(cfg),
		MaxOpenConns:  cfg.Storage.MaxOpenConns,
		BusyTimeoutMs: cfg.Storage.BusyTimeoutMs,
	}, log)
	if err != nil {
		return BenchmarkRun{}, fmt.Errorf("open store: %w", err)
	}
	defer db.Close()

	embedClient := ollama.New(ollama.Config{
		BaseURL: cfg.Ollama.BaseURL,
		Model:   cfg.Ollama.EmbeddingModel,
		Timeout: cfg.Ollama.Timeout,
	}, log)
	pipeline := retrieval.New(db, embedClient, retrieval.Config{
		SemanticWeight:         cfg.Retrieval.SemanticWeight,
		RecencyWeight:          cfg.Retrieval.RecencyWeight,
		CategoryWeight:         cfg.Retrieval.CategoryWeight,
		ConfidenceWeight:       cfg.Retrieval.ConfidenceWeight,
		RecencyHalfLifeDays:    cfg.Retrieval.RecencyHalfLifeDays,
		DefaultLimit:           cfg.Retrieval.DefaultLimit,
		MinConfidenceThreshold: cfg.Retrieval.MinConfidenceThreshold,
	}, log)
	service := app.NewMemoryService(db, pipeline, embedClient, log)

	candidateLimit := maxQueries * 12
	if candidateLimit < 100 {
		candidateLimit = 100
	}
	memories, err := service.List(ctx, memory.ListOptions{Limit: candidateLimit, MinConfidence: 0.30})
	if err != nil {
		return BenchmarkRun{}, fmt.Errorf("list live memories: %w", err)
	}
	queries := buildLiveQueries(memories, maxQueries)
	if len(queries) == 0 {
		return BenchmarkRun{}, fmt.Errorf("not enough live memories to derive benchmark queries")
	}

	now := time.Now()
	run := BenchmarkRun{
		Version:   version,
		Timestamp: now,
		Config: ScoringConfig{
			SemanticWeight:      cfg.Retrieval.SemanticWeight,
			RecencyWeight:       cfg.Retrieval.RecencyWeight,
			CategoryWeight:      cfg.Retrieval.CategoryWeight,
			ConfidenceWeight:    cfg.Retrieval.ConfidenceWeight,
			RecencyHalfLifeDays: cfg.Retrieval.RecencyHalfLifeDays,
			EmbeddingModel:      "live:" + cfg.Ollama.EmbeddingModel,
		},
		CorpusSize: len(memories),
		QueryCount: len(queries),
	}

	var totalP, totalR, totalMRR, totalNDCG float64
	for _, q := range queries {
		results, err := service.Search(ctx, q.text, app.SearchOptions{Limit: q.k})
		if err != nil {
			return BenchmarkRun{}, fmt.Errorf("live search %q: %w", q.text, err)
		}
		scored := make([]scoredMemory, 0, len(results))
		for _, result := range results {
			if result.Memory == nil {
				continue
			}
			scored = append(scored, scoredMemory{id: result.Memory.ID, score: result.FinalScore})
		}
		m := computeMetrics(scored, q.relevant, q.k)
		totalP += m.PrecisionAtK
		totalR += m.RecallAtK
		totalMRR += m.MRR
		totalNDCG += m.NDCGAtK
		run.Results = append(run.Results, QueryResult{
			Query:        q.text,
			PrecisionAtK: m.PrecisionAtK,
			RecallAtK:    m.RecallAtK,
			MRR:          m.MRR,
			NDCGAtK:      m.NDCGAtK,
			K:            q.k,
		})
	}

	n := float64(len(queries))
	run.Aggregate = AggregateMetrics{
		MeanPrecisionAtK: totalP / n,
		MeanRecallAtK:    totalR / n,
		MeanMRR:          totalMRR / n,
		MeanNDCG:         totalNDCG / n,
	}
	return run, nil
}

func printReport(run BenchmarkRun) {
	fmt.Printf("\n╔══════════════════════════════════════════════════════════════════╗\n")
	fmt.Printf("║  NEXUS RETRIEVAL BENCHMARK — %s                          ║\n", run.Version)
	fmt.Printf("╠══════════════════════════════════════════════════════════════════╣\n")
	fmt.Printf("║  Corpus: %d memories │ Queries: %d │ %s\n", run.CorpusSize, run.QueryCount, run.Timestamp.Format("2006-01-02"))
	fmt.Printf("║  Weights: sem=%.2f rec=%.2f cat=%.2f conf=%.2f\n",
		run.Config.SemanticWeight, run.Config.RecencyWeight,
		run.Config.CategoryWeight, run.Config.ConfidenceWeight)
	fmt.Printf("╠══════════════════════════════════════════════════════════════════╣\n")
	fmt.Printf("║  %-38s  P@K   Recall   MRR   NDCG\n", "Query")
	fmt.Printf("╠══════════════════════════════════════════════════════════════════╣\n")

	for _, r := range run.Results {
		q := r.Query
		if len(q) > 38 {
			q = q[:35] + "..."
		}
		fmt.Printf("║  %-38s  %.3f  %.3f   %.3f  %.3f\n",
			q, r.PrecisionAtK, r.RecallAtK, r.MRR, r.NDCGAtK)
	}

	a := run.Aggregate
	fmt.Printf("╠══════════════════════════════════════════════════════════════════╣\n")
	fmt.Printf("║  %-38s  %.3f  %.3f   %.3f  %.3f\n",
		"AVERAGE", a.MeanPrecisionAtK, a.MeanRecallAtK, a.MeanMRR, a.MeanNDCG)
	fmt.Printf("╚══════════════════════════════════════════════════════════════════╝\n")
}

func printComparison(prev, curr BenchmarkRun) {
	mrrDelta := curr.Aggregate.MeanMRR - prev.Aggregate.MeanMRR
	ndcgDelta := curr.Aggregate.MeanNDCG - prev.Aggregate.MeanNDCG
	pDelta := curr.Aggregate.MeanPrecisionAtK - prev.Aggregate.MeanPrecisionAtK

	sign := func(f float64) string {
		if f >= 0 {
			return fmt.Sprintf("+%.3f", f)
		}
		return fmt.Sprintf("%.3f", f)
	}

	fmt.Printf("\n── Δ vs %s ─────────────────────────────\n", prev.Version)
	fmt.Printf("  MRR:       %s  (%.3f → %.3f)\n", sign(mrrDelta), prev.Aggregate.MeanMRR, curr.Aggregate.MeanMRR)
	fmt.Printf("  NDCG:      %s  (%.3f → %.3f)\n", sign(ndcgDelta), prev.Aggregate.MeanNDCG, curr.Aggregate.MeanNDCG)
	fmt.Printf("  Precision: %s  (%.3f → %.3f)\n", sign(pDelta), prev.Aggregate.MeanPrecisionAtK, curr.Aggregate.MeanPrecisionAtK)
	fmt.Println()
}

func loadRun(path string) (*BenchmarkRun, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var run BenchmarkRun
	return &run, json.Unmarshal(b, &run)
}

// ---- Corpus + query definitions ----

type corpusMem struct {
	id         string
	category   string
	content    string
	confidence float64
	embedding  []float32
	updatedAt  time.Time
}

type evalQuery struct {
	text       string
	embIdx     int
	relevant   map[string]int
	filterCats []string
	k          int
}

type scoredMemory struct {
	id    string
	score float64
}

func buildCorpus() []corpusMem {
	now := time.Now()
	raw := []struct {
		id, cat, content string
		embIdx           int
		daysAgo          int
	}{
		{"c01", "CODING_STYLE", "Prefers early returns over nested if-else in Go", 1, 2},
		{"c02", "CODING_STYLE", "Uses table-driven tests with t.Run subtests", 2, 5},
		{"c03", "CODING_STYLE", "Avoids global state, prefers dependency injection", 3, 10},
		{"c04", "CODING_STYLE", "Writes explicit error handling, never ignores errors", 4, 3},
		{"f01", "FACT", "Primary programming language is Go", 10, 30},
		{"f02", "FACT", "Works at RubixKube as SRE-focused engineer", 11, 30},
		{"f03", "FACT", "Uses GoLand as primary IDE", 12, 15},
		{"f04", "FACT", "Develops on Windows, deploys to Linux", 13, 20},
		{"p01", "PROJECT", "Nexus: personal context engine and AI memory layer", 20, 1},
		{"p02", "PROJECT", "Nexus uses SQLite with FTS5 for storage", 21, 1},
		{"p03", "PROJECT", "Nexus uses Ollama nomic-embed-text for embeddings", 22, 2},
		{"p04", "PROJECT", "Nexus MCP server uses mark3labs/mcp-go", 23, 3},
		{"p05", "PROJECT", "memory-engine-x is a distributed systems project", 24, 7},
		{"p06", "PROJECT", "webhook-observer monitors webhook delivery", 25, 7},
		{"w01", "WORKFLOW", "Runs go test ./... before every commit", 30, 5},
		{"w02", "WORKFLOW", "Uses Makefile targets for build and dev tasks", 31, 8},
		{"w03", "WORKFLOW", "Reviews git diff before staging changes", 32, 4},
		{"pr01", "PREFERENCE", "Prefers concise dense technical communication", 40, 20},
		{"pr02", "PREFERENCE", "Prefers dark mode in all development tools", 41, 25},
	}

	corpus := make([]corpusMem, len(raw))
	for i, r := range raw {
		corpus[i] = corpusMem{
			id:         r.id,
			category:   r.cat,
			content:    r.content,
			confidence: 1.0,
			embedding:  pseudoEmbed(r.embIdx, 768),
			updatedAt:  now.Add(-time.Duration(r.daysAgo) * 24 * time.Hour),
		}
	}
	return corpus
}

func buildQueries() []evalQuery {
	return []evalQuery{
		{
			text:     "how does this developer write Go code",
			embIdx:   2,
			relevant: map[string]int{"c01": 2, "c02": 2, "c03": 2, "c04": 2, "f01": 1},
			k:        5,
		},
		{
			text:     "what is Nexus and how does it store data",
			embIdx:   21,
			relevant: map[string]int{"p01": 2, "p02": 2, "p03": 1, "p04": 1},
			k:        5,
		},
		{
			text:     "what IDE and tools does this engineer use",
			embIdx:   12,
			relevant: map[string]int{"f03": 2, "f04": 1, "w02": 1},
			k:        5,
		},
		{
			text:     "developer testing and commit habits",
			embIdx:   30,
			relevant: map[string]int{"w01": 2, "w02": 1, "w03": 1, "c02": 1},
			k:        5,
		},
		{
			text:     "what projects is this developer working on",
			embIdx:   24,
			relevant: map[string]int{"p01": 2, "p05": 2, "p06": 2, "p02": 1, "p03": 1},
			k:        5,
		},
	}
}

func buildLiveQueries(memories []*memory.Memory, maxQueries int) []evalQuery {
	queries := make([]evalQuery, 0, maxQueries)
	seen := make(map[string]struct{}, maxQueries)
	for _, m := range memories {
		if m == nil || strings.TrimSpace(m.Content) == "" {
			continue
		}
		query := liveQueryFromMemory(m.Content)
		if query == "" {
			continue
		}
		key := strings.ToLower(query)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		queries = append(queries, evalQuery{
			text:     query,
			relevant: map[string]int{m.ID: 2},
			k:        5,
		})
		if len(queries) >= maxQueries {
			break
		}
	}
	return queries
}

func liveQueryFromMemory(content string) string {
	content = strings.Join(strings.Fields(content), " ")
	if len(content) < 48 {
		return ""
	}
	words := strings.FieldsFunc(strings.ToLower(content), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-')
	})
	filtered := make([]string, 0, 16)
	seen := make(map[string]struct{}, len(words))
	for _, word := range words {
		if len(word) < 3 || liveStopWords[word] || !hasASCIILetter(word) {
			continue
		}
		if _, ok := seen[word]; ok {
			continue
		}
		seen[word] = struct{}{}
		filtered = append(filtered, word)
	}
	if len(filtered) < 5 {
		return ""
	}
	return strings.Join(selectSpreadTerms(filtered, 8), " ")
}

func selectSpreadTerms(terms []string, max int) []string {
	if len(terms) <= max {
		return terms
	}
	selected := make([]string, 0, max)
	used := make(map[int]struct{}, max)
	step := float64(len(terms)-1) / float64(max-1)
	for i := 0; i < max; i++ {
		idx := int(math.Round(float64(i) * step))
		if _, ok := used[idx]; ok {
			continue
		}
		used[idx] = struct{}{}
		selected = append(selected, terms[idx])
	}
	return selected
}

func hasASCIILetter(word string) bool {
	for _, r := range word {
		if r >= 'a' && r <= 'z' {
			return true
		}
	}
	return false
}

var liveStopWords = map[string]bool{
	"and": true, "are": true, "but": true, "for": true, "from": true, "has": true,
	"into": true, "not": true, "that": true, "the": true, "this": true, "with": true,
	"would": true, "should": true, "likely": true, "prefers": true, "user": true,
	"memory": true, "memories": true, "project": true,
}

// ---- Metrics ----

type metrics struct {
	PrecisionAtK float64
	RecallAtK    float64
	MRR          float64
	NDCGAtK      float64
}

func computeMetrics(results []scoredMemory, relevant map[string]int, k int) metrics {
	if len(results) == 0 || k <= 0 {
		return metrics{}
	}
	if k > len(results) {
		k = len(results)
	}
	topK := results[:k]

	hits := 0
	for _, r := range topK {
		if relevant[r.id] > 0 {
			hits++
		}
	}

	mrr := 0.0
	for rank, r := range topK {
		if relevant[r.id] > 0 {
			mrr = 1.0 / float64(rank+1)
			break
		}
	}

	dcg := 0.0
	for rank, r := range topK {
		dcg += float64(relevant[r.id]) / math.Log2(float64(rank)+2)
	}

	grades := make([]int, 0, len(relevant))
	for _, g := range relevant {
		grades = append(grades, g)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(grades)))

	idcg := 0.0
	for rank, g := range grades {
		if rank >= k {
			break
		}
		idcg += float64(g) / math.Log2(float64(rank)+2)
	}

	ndcg := 0.0
	if idcg > 0 {
		ndcg = dcg / idcg
	}

	totalRelevant := len(relevant)
	recall := 0.0
	if totalRelevant > 0 {
		recall = float64(hits) / float64(totalRelevant)
	}

	return metrics{
		PrecisionAtK: float64(hits) / float64(k),
		RecallAtK:    recall,
		MRR:          mrr,
		NDCGAtK:      ndcg,
	}
}

// ---- Scoring functions ----

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		fa, fb := float64(a[i]), float64(b[i])
		dot += fa * fb
		na += fa * fa
		nb += fb * fb
	}
	if na == 0 || nb == 0 {
		return 0
	}
	s := dot / (math.Sqrt(na) * math.Sqrt(nb))
	if s > 1.0 {
		return 1.0
	}
	if s < 0 {
		return 0
	}
	return s
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

func categoryScore(cat string, filter []string) float64 {
	if len(filter) > 0 {
		for _, f := range filter {
			if f == cat {
				return 1.0
			}
		}
		return 0.3
	}
	weights := map[string]float64{
		"FACT": 0.85, "PROJECT": 0.85, "WORKFLOW": 0.75,
		"CODING_STYLE": 0.70, "PREFERENCE": 0.65, "INFERRED": 0.50,
	}
	if w, ok := weights[cat]; ok {
		return w
	}
	return 0.5
}

func pseudoEmbed(idx int, dims int) []float32 {
	v := make([]float32, dims)
	var norm float64
	for i := range v {
		phase := float64(idx)*0.1 + float64(i)*0.01
		v[i] = float32(math.Sin(phase)*0.5 + 0.5)
		norm += float64(v[i]) * float64(v[i])
	}
	norm = math.Sqrt(norm)
	if norm > 0 {
		for i := range v {
			v[i] /= float32(norm)
		}
	}
	return v
}

func resolveBenchmarkDBPath(cfg *nexusconfig.Config) string {
	dbPath := strings.TrimSpace(cfg.Storage.DBPath)
	if dbPath != "" {
		return expandBenchmarkHomePath(dbPath)
	}
	dataDir := strings.TrimSpace(cfg.Storage.DataDir)
	if dataDir == "" {
		dataDir = filepath.Join(benchmarkHomeDir(), ".nexus")
	}
	return filepath.Join(expandBenchmarkHomePath(dataDir), "nexus.db")
}

func expandBenchmarkHomePath(path string) string {
	home := benchmarkHomeDir()
	if home != "" {
		path = strings.ReplaceAll(path, "${HOME}", home)
		path = strings.ReplaceAll(path, "$HOME", home)
		if path == "~" {
			path = home
		} else if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
			path = filepath.Join(home, path[2:])
		}
		if path == "/.nexus" || path == `\.nexus` {
			path = filepath.Join(home, ".nexus")
		}
		if strings.HasPrefix(path, "/.nexus/") || strings.HasPrefix(path, `\.nexus\`) {
			path = filepath.Join(home, ".nexus", path[len("/.nexus/"):])
		}
	}
	return os.ExpandEnv(path)
}

func benchmarkHomeDir() string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return home
	}
	return os.Getenv("USERPROFILE")
}
