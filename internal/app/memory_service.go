package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/neeldholiya04/nexus/internal/memory"
)

type Store interface {
	Insert(ctx context.Context, m *memory.Memory) error
	Update(ctx context.Context, m *memory.Memory) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, opts memory.ListOptions) ([]*memory.Memory, error)
	FTSSearch(ctx context.Context, query string, limit int) ([]*memory.Memory, error)
	GetUnembedded(ctx context.Context, limit int) ([]*memory.Memory, error)
	UpdateEmbedding(ctx context.Context, id string, embedding []float32) error
	Stats(ctx context.Context) (*memory.StoreStats, error)
	UpsertArchetype(ctx context.Context, a *memory.Archetype) error
	ListArchetypes(ctx context.Context) ([]*memory.Archetype, error)
	UpsertPersona(ctx context.Context, p *memory.Persona) error
	ListPersonas(ctx context.Context) ([]*memory.Persona, error)
	RecordSession(ctx context.Context, session *memory.Session) error
	UpsertProject(ctx context.Context, p *memory.Project) error
	ListProjects(ctx context.Context) ([]*memory.Project, error)
}

type Retriever interface {
	Retrieve(ctx context.Context, q memory.Query) ([]memory.RetrievalResult, error)
}

type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	Ping(ctx context.Context) error
}

type MemoryService struct {
	store     Store
	retriever Retriever
	embedder  Embedder
	log       *zap.Logger
	newID     func() string
	upsertMu  sync.Mutex
}

func NewMemoryService(store Store, retriever Retriever, embedder Embedder, log *zap.Logger) *MemoryService {
	if log == nil {
		log = zap.NewNop()
	}
	return &MemoryService{
		store:     store,
		retriever: retriever,
		embedder:  embedder,
		log:       log,
		newID:     func() string { return uuid.New().String() },
	}
}

func (s *MemoryService) SetIDGenerator(newID func() string) {
	if newID != nil {
		s.newID = newID
	}
}

type AddMemoryInput struct {
	Content    string
	Category   memory.Category
	Layer      memory.Layer
	PersonaID  string
	Source     memory.Source
	Confidence float64
	Tags       []string
	Metadata   map[string]any
	ForceNew   bool
}

type AddMemoryResult struct {
	Memory              *memory.Memory
	Inserted            bool
	Updated             bool
	Embedded            bool
	EmbeddingErr        error
	DuplicateID         string
	DuplicateSimilarity float64
}

func (s *MemoryService) AddMemory(ctx context.Context, in AddMemoryInput) (*AddMemoryResult, error) {
	m, err := s.newMemory(in)
	if err != nil {
		return nil, err
	}
	if !in.ForceNew {
		result, err := s.UpsertMemory(ctx, m)
		if err != nil {
			return nil, err
		}
		return &AddMemoryResult{
			Memory:              result.Memory,
			Inserted:            result.Inserted,
			Updated:             result.Updated,
			Embedded:            result.Embedded,
			EmbeddingErr:        result.EmbeddingErr,
			DuplicateID:         result.DuplicateID,
			DuplicateSimilarity: result.DuplicateSimilarity,
		}, nil
	}

	if err := s.store.Insert(ctx, m); err != nil {
		return nil, fmt.Errorf("add memory: %w", err)
	}

	embedded, embedErr := s.embedAndUpdate(ctx, m)
	return &AddMemoryResult{Memory: m, Inserted: true, Embedded: embedded, EmbeddingErr: embedErr}, nil
}

type SearchOptions struct {
	Category memory.Category
	Limit    int
}

func (s *MemoryService) Search(ctx context.Context, text string, opts SearchOptions) ([]memory.RetrievalResult, error) {
	if s.retriever == nil {
		return nil, errors.New("memory service: retriever is not configured")
	}
	q := memory.DefaultQuery(text)
	q.Limit = opts.Limit
	if opts.Category != "" {
		if !opts.Category.Valid() {
			return nil, fmt.Errorf("invalid category %q", opts.Category)
		}
		q.Categories = []memory.Category{opts.Category}
	}
	return s.retriever.Retrieve(ctx, q)
}

func (s *MemoryService) List(ctx context.Context, opts memory.ListOptions) ([]*memory.Memory, error) {
	for _, cat := range opts.Categories {
		if !cat.Valid() {
			return nil, fmt.Errorf("invalid category %q", cat)
		}
	}
	return s.store.List(ctx, opts)
}

func (s *MemoryService) Delete(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("memory service: id cannot be empty")
	}
	return s.store.Delete(ctx, id)
}

func (s *MemoryService) Stats(ctx context.Context) (*memory.StoreStats, error) {
	return s.store.Stats(ctx)
}

type UpsertMemoryResult struct {
	Memory              *memory.Memory
	Inserted            bool
	Updated             bool
	Embedded            bool
	EmbeddingErr        error
	DuplicateID         string
	DuplicateSimilarity float64
}

type upsertDecision struct {
	result         *UpsertMemoryResult
	memoryToEmbed  *memory.Memory
	contentChanged bool
}

func (s *MemoryService) UpsertMemory(ctx context.Context, incoming *memory.Memory) (*UpsertMemoryResult, error) {
	if incoming == nil {
		return nil, errors.New("upsert memory: memory cannot be nil")
	}
	if err := s.prepareMemory(incoming); err != nil {
		return nil, err
	}

	decision, err := s.upsertMemoryLocked(ctx, incoming)
	if err != nil {
		return nil, err
	}

	if decision.memoryToEmbed != nil && (decision.result.Inserted || decision.contentChanged) {
		embedded, embedErr := s.embedAndUpdate(ctx, decision.memoryToEmbed)
		decision.result.Embedded = embedded
		decision.result.EmbeddingErr = embedErr
	}
	return decision.result, nil
}

func (s *MemoryService) upsertMemoryLocked(ctx context.Context, incoming *memory.Memory) (*upsertDecision, error) {
	s.upsertMu.Lock()
	defer s.upsertMu.Unlock()

	existing, similarity, err := s.findDuplicate(ctx, incoming)
	if err != nil {
		s.log.Warn("upsert memory: duplicate lookup failed, inserting fresh", zap.Error(err))
		if err := s.store.Insert(ctx, incoming); err != nil {
			return nil, fmt.Errorf("upsert memory: insert fallback: %w", err)
		}
		return &upsertDecision{
			result:        &UpsertMemoryResult{Memory: incoming, Inserted: true},
			memoryToEmbed: incoming,
		}, nil
	}

	if existing != nil {
		contentChanged := reinforceMemory(existing, incoming)
		if err := s.store.Update(ctx, existing); err != nil {
			return nil, fmt.Errorf("upsert memory: update duplicate %q: %w", existing.ID, err)
		}

		return &upsertDecision{
			result: &UpsertMemoryResult{
				Memory:              existing,
				Updated:             true,
				DuplicateID:         existing.ID,
				DuplicateSimilarity: similarity,
			},
			memoryToEmbed:  existing,
			contentChanged: contentChanged,
		}, nil
	}

	if err := s.store.Insert(ctx, incoming); err != nil {
		return nil, fmt.Errorf("upsert memory: insert: %w", err)
	}
	return &upsertDecision{
		result:        &UpsertMemoryResult{Memory: incoming, Inserted: true},
		memoryToEmbed: incoming,
	}, nil
}

func (s *MemoryService) findDuplicate(ctx context.Context, incoming *memory.Memory) (*memory.Memory, float64, error) {
	if existing, similarity, err := s.findProjectDuplicate(ctx, incoming); err != nil {
		s.log.Warn("upsert memory: project duplicate lookup failed", zap.Error(err))
	} else if existing != nil {
		return existing, similarity, nil
	}

	candidates, err := s.store.FTSSearch(ctx, memory.FTSQuerySafe(incoming.Content), 5)
	if err != nil {
		return nil, 0, err
	}

	for _, existing := range candidates {
		if existing.Category != incoming.Category {
			continue
		}
		if existing.Layer() != incoming.Layer() {
			continue
		}
		similarity := jaccardSimilarity(existing.Content, incoming.Content)
		if similarity < duplicateThreshold(incoming.Category) {
			continue
		}
		return existing, similarity, nil
	}

	return nil, 0, nil
}

func (s *MemoryService) findProjectDuplicate(ctx context.Context, incoming *memory.Memory) (*memory.Memory, float64, error) {
	if incoming.Category != memory.CategoryProject {
		return nil, 0, nil
	}
	incomingKey, ok := projectIdentity(incoming)
	if !ok {
		return nil, 0, nil
	}

	existing, err := s.store.List(ctx, memory.ListOptions{
		Categories: []memory.Category{memory.CategoryProject},
		Limit:      500,
	})
	if err != nil {
		return nil, 0, err
	}

	for _, candidate := range existing {
		if candidate.Layer() != incoming.Layer() {
			continue
		}
		candidateKey, ok := projectIdentity(candidate)
		if ok && candidateKey == incomingKey {
			return candidate, 1.0, nil
		}
	}
	return nil, 0, nil
}

func (s *MemoryService) BackfillEmbeddings(ctx context.Context, batchSize int, onProgress func(total int)) (int, error) {
	if s.embedder == nil {
		return 0, errors.New("embedder is not configured")
	}
	if batchSize <= 0 {
		batchSize = 20
	}
	if err := s.embedder.Ping(ctx); err != nil {
		return 0, fmt.Errorf("embedding service not ready: %w", err)
	}

	total := 0
	for {
		batch, err := s.store.GetUnembedded(ctx, batchSize)
		if err != nil {
			return total, fmt.Errorf("get unembedded batch: %w", err)
		}
		if len(batch) == 0 {
			break
		}

		texts := make([]string, len(batch))
		for i, m := range batch {
			texts[i] = m.Content
		}

		vecs, err := s.embedder.EmbedBatch(ctx, texts)
		if err != nil {
			return total, fmt.Errorf("embed batch: %w", err)
		}
		if len(vecs) != len(batch) {
			return total, fmt.Errorf("embed batch: expected %d vectors, got %d", len(batch), len(vecs))
		}

		for i, m := range batch {
			if err := s.store.UpdateEmbedding(ctx, m.ID, vecs[i]); err != nil {
				s.log.Warn("backfill embeddings: update failed", zap.String("id", m.ID), zap.Error(err))
			}
		}

		total += len(batch)
		if onProgress != nil {
			onProgress(total)
		}
		if len(batch) < batchSize {
			break
		}
	}
	return total, nil
}

func (s *MemoryService) newMemory(in AddMemoryInput) (*memory.Memory, error) {
	m := &memory.Memory{
		ID:         s.newID(),
		Category:   in.Category,
		Content:    in.Content,
		Source:     in.Source,
		Confidence: in.Confidence,
		Tags:       in.Tags,
		Metadata:   cloneMetadata(in.Metadata),
	}
	if in.Layer != "" {
		m.SetLayer(in.Layer)
	}
	m.SetPersonaID(in.PersonaID)
	if err := s.prepareMemory(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *MemoryService) prepareMemory(m *memory.Memory) error {
	m.Content = strings.TrimSpace(m.Content)
	if m.Content == "" {
		return errors.New("memory content cannot be empty")
	}
	if !m.Category.Valid() {
		return fmt.Errorf("invalid category %q", m.Category)
	}
	if m.ID == "" {
		m.ID = s.newID()
	}
	if m.Source == "" {
		m.Source = memory.SourceManual
	}
	if m.Confidence <= 0 {
		m.Confidence = memory.DefaultConfidence(m.Source)
	}
	if m.Confidence > 1.0 {
		m.Confidence = 1.0
	}
	m.Tags = normalizeTags(m.Tags)
	if m.Metadata == nil {
		m.Metadata = map[string]any{}
	}
	m.SetLayer(m.Layer())
	if m.Layer() == memory.LayerDynamic && m.PersonaID() == "" {
		m.SetPersonaID("default")
	}
	if m.Layer() == memory.LayerDynamic && m.ValidUntil == nil {
		validUntil := time.Now().UTC().Add(defaultDynamicTTL(m.Category))
		m.ValidUntil = &validUntil
	}
	return nil
}

func (s *MemoryService) embedAndUpdate(ctx context.Context, m *memory.Memory) (bool, error) {
	if s.embedder == nil {
		return false, nil
	}
	vec, err := s.embedder.Embed(ctx, m.Content)
	if err != nil {
		s.log.Warn("memory embedding failed", zap.String("id", m.ID), zap.Error(err))
		return false, err
	}
	if err := s.store.UpdateEmbedding(ctx, m.ID, vec); err != nil {
		s.log.Warn("memory embedding update failed", zap.String("id", m.ID), zap.Error(err))
		return false, err
	}
	m.Embedding = vec
	return true, nil
}

func reinforceMemory(existing, incoming *memory.Memory) bool {
	originalConf := existing.Confidence
	newConf := existing.Confidence + 0.05
	if incoming.Confidence > newConf {
		newConf = incoming.Confidence
	}
	if newConf > 1.0 {
		newConf = 1.0
	}
	existing.Confidence = newConf
	existing.Tags = mergeTags(existing.Tags, incoming.Tags)
	existing.Metadata = mergeMetadata(existing.Metadata, incoming.Metadata)
	if existing.ValidFrom == nil && incoming.ValidFrom != nil {
		existing.ValidFrom = incoming.ValidFrom
	}
	if incoming.ValidUntil != nil && (existing.ValidUntil == nil || incoming.ValidUntil.After(*existing.ValidUntil)) {
		existing.ValidUntil = incoming.ValidUntil
	}

	incomingContent := strings.TrimSpace(incoming.Content)
	if incoming.Category == memory.CategoryProject && incomingContent != "" && incomingContent != existing.Content && incoming.Confidence >= originalConf {
		if existingKey, ok := projectIdentity(existing); ok {
			if incomingKey, ok := projectIdentity(incoming); ok && incomingKey == existingKey {
				existing.Content = incomingContent
				return true
			}
		}
	}
	if incomingContent != "" && incomingContent != existing.Content && incoming.Confidence > originalConf+0.10 {
		existing.Content = incomingContent
		return true
	}
	return false
}

func normalizeTags(tags []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

func mergeTags(a, b []string) []string {
	merged := make([]string, 0, len(a)+len(b))
	merged = append(merged, a...)
	merged = append(merged, b...)
	return normalizeTags(merged)
}

func mergeMetadata(a, b map[string]any) map[string]any {
	merged := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		merged[k] = v
	}
	for k, v := range b {
		merged[k] = v
	}
	return merged
}

func cloneMetadata(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func defaultDynamicTTL(category memory.Category) time.Duration {
	switch category {
	case memory.CategoryProject:
		return 60 * 24 * time.Hour
	case memory.CategoryWorkflow:
		return 21 * 24 * time.Hour
	default:
		return 30 * 24 * time.Hour
	}
}

func duplicateThreshold(category memory.Category) float64 {
	if category == memory.CategoryProject {
		return 0.55
	}
	return 0.65
}

func jaccardSimilarity(a, b string) float64 {
	setA := wordSet(a)
	setB := wordSet(b)
	if len(setA) == 0 && len(setB) == 0 {
		return 1.0
	}

	intersection := 0
	for w := range setA {
		if _, ok := setB[w]; ok {
			intersection++
		}
	}
	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func wordSet(s string) map[string]struct{} {
	set := make(map[string]struct{})
	word := strings.Builder{}
	for _, ch := range strings.ToLower(s) {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			word.WriteRune(ch)
			continue
		}
		if word.Len() > 1 {
			set[word.String()] = struct{}{}
		}
		word.Reset()
	}
	if word.Len() > 1 {
		set[word.String()] = struct{}{}
	}
	return set
}

var projectNamePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bproject\s*:\s*([^.;,\n\r]+?)(?:\s+at\s+|[.;,\n\r]|$)`),
	regexp.MustCompile(`(?i)\b(?:current|main|active)?\s*(?:project|repo|repository|workspace)\s+(?:is|called|named)\s+([^.;,\n\r]+?)(?:\s+at\s+|[.;,\n\r]|$)`),
	regexp.MustCompile(`\b[Pp]roject\s+([A-Z][A-Za-z0-9_.-]*(?:[ -][A-Z0-9][A-Za-z0-9_.-]*){0,5})(?:\s+(?:at|uses|has|is|was|will|should|must)\b|[.;,\n\r]|$)`),
}
var projectPathStartPattern = regexp.MustCompile(`[A-Za-z]:[\\/]`)

func projectIdentity(m *memory.Memory) (string, bool) {
	if m == nil {
		return "", false
	}
	for _, key := range []string{"project_path", "repo_path", "workspace_path", "path"} {
		if value, ok := metadataString(m.Metadata, key); ok {
			if normalized := normalizeProjectPath(value); normalized != "" {
				return "path:" + normalized, true
			}
		}
	}
	for _, key := range []string{"project_name", "repo_name", "workspace_name", "name"} {
		if value, ok := metadataString(m.Metadata, key); ok {
			if normalized := normalizeProjectName(value); normalized != "" {
				return "name:" + normalized, true
			}
		}
	}
	for _, tag := range m.Tags {
		lowerTag := strings.ToLower(tag)
		if strings.HasPrefix(lowerTag, "path:") {
			if normalized := normalizeProjectPath(strings.TrimSpace(tag[len("path:"):])); normalized != "" {
				return "path:" + normalized, true
			}
			continue
		}
		for _, prefix := range []string{"project:", "repo:", "workspace:"} {
			if !strings.HasPrefix(lowerTag, prefix) {
				continue
			}
			if normalized := normalizeProjectName(strings.TrimSpace(tag[len(prefix):])); normalized != "" {
				return "name:" + normalized, true
			}
		}
	}
	if path := extractProjectPath(m.Content); path != "" {
		return "path:" + normalizeProjectPath(path), true
	}
	if name := extractProjectName(m.Content); name != "" {
		return "name:" + normalizeProjectName(name), true
	}
	return "", false
}

func metadataString(metadata map[string]any, key string) (string, bool) {
	if metadata == nil {
		return "", false
	}
	value, ok := metadata[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	return text, text != ""
}

func extractProjectPath(text string) string {
	loc := projectPathStartPattern.FindStringIndex(text)
	if loc == nil {
		return ""
	}
	start := loc[0]
	end := len(text)
	for offset, r := range text[start:] {
		idx := start + offset
		switch r {
		case '\n', '\r', '\t', ',', ';', ')', ']', '}':
			end = idx
			return strings.TrimRight(text[start:end], ". ")
		case '.':
			next := idx + 1
			if next >= len(text) || text[next] == ' ' {
				end = idx
				return strings.TrimRight(text[start:end], ". ")
			}
		}
	}
	return strings.TrimRight(text[start:end], ". ")
}

func extractProjectName(text string) string {
	for _, pattern := range projectNamePatterns {
		match := pattern.FindStringSubmatch(text)
		if len(match) >= 2 {
			if name := normalizeProjectName(match[1]); name != "" {
				return name
			}
		}
	}
	return ""
}

func normalizeProjectPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, " \t\r\n\"'`.,;()[]{}")
	path = strings.ReplaceAll(path, "/", "\\")
	for strings.Contains(path, "\\\\") {
		path = strings.ReplaceAll(path, "\\\\", "\\")
	}
	path = strings.TrimRight(path, "\\")
	path = strings.Join(strings.Fields(path), " ")
	return strings.ToLower(path)
}

func normalizeProjectName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Trim(name, " \t\r\n\"'`.,;:()[]{}")
	name = strings.Join(strings.Fields(name), " ")
	return strings.ToLower(name)
}
