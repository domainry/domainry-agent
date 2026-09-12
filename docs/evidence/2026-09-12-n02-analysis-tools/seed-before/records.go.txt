package businessseed

import (
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"

	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/requestcontext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type manifestBusinessSeedRow struct {
	Key         string
	ObjectKey   string
	DataJSON    string
	OwnerUserID string
	OwnerOrgID  string
	SourceKind  string
	SourceID    string
}

func SyncManifestBusinessSeeds(ctx context.Context, records recordrepository.RecordBusinessSeedRepository, manifest manifestmodel.ManifestSchema, rows []manifestBusinessSeedRow) error {
	if records == nil || len(rows) == 0 {
		return nil
	}
	objectByKey := manifestBusinessSeedObjects(manifest)
	workspaceID := requestcontext.WorkspaceID(ctx)
	if workspaceID == "" {
		workspaceID = principalmodel.InstallationWorkspaceID
	}
	targets, err := manifestBusinessSeedTargetStates(ctx, records, workspaceID, objectByKey, rows)
	if err != nil {
		return err
	}
	ordered, err := orderManifestBusinessSeedRows(rows)
	if err != nil {
		return err
	}
	idsByKey := manifestBusinessSeedIDs(rows)
	for _, row := range rows {
		state := targets[row.ObjectKey]
		if state.HasRecords && strings.TrimSpace(state.FirstRecordID) != "" {
			idsByKey[strings.TrimSpace(row.Key)] = strings.TrimSpace(state.FirstRecordID)
		}
	}
	for _, row := range ordered {
		if targets[row.ObjectKey].HasRecords {
			continue
		}
		for _, ref := range collectManifestBusinessSeedRefs(row.DataJSON) {
			if strings.TrimSpace(idsByKey[ref]) == "" {
				return fmt.Errorf("resolve domain seed %s/%s: existing target for %s has no readable record id", row.ObjectKey, row.Key, ref)
			}
		}
	}
	runID := manifestBusinessSeedRunID()
	now := time.Now().UTC().Format(time.RFC3339)
	for _, row := range ordered {
		object, ok := objectByKey[row.ObjectKey]
		if !ok {
			continue
		}
		if targets[row.ObjectKey].HasRecords {
			continue
		}
		data := map[string]any{}
		if strings.TrimSpace(row.DataJSON) != "" {
			if err := json.Unmarshal([]byte(row.DataJSON), &data); err != nil {
				return fmt.Errorf("decode domain seed %s/%s: %w", row.ObjectKey, row.Key, err)
			}
		}
		data = filterManifestBusinessSeedData(object, resolveManifestBusinessSeedPlaceholders(resolveManifestBusinessSeedRefs(data, idsByKey), runID))
		record := recordmodel.Record{
			ID:          idsByKey[row.Key],
			Data:        data,
			OwnerUserID: strings.TrimSpace(row.OwnerUserID),
			OwnerOrgID:  strings.TrimSpace(row.OwnerOrgID),
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if strings.TrimSpace(record.ID) == "" {
			record.ID = manifestBusinessSeedRecordID(row.ObjectKey, row.Key)
		}
		if err := records.InsertRecord(ctx, workspaceID, object, record); err != nil {
			// Multiple Runtime instances may observe an empty table at the same
			// time. Generated IDs are deterministic, so an already-present row
			// means the concurrent bootstrap converged on the same result.
			if _, found, lookupErr := records.GetRecord(ctx, workspaceID, object, record.ID); lookupErr == nil && found {
				continue
			}
			return fmt.Errorf("sync domain seed %s/%s: %w", row.ObjectKey, row.Key, err)
		}
	}
	return nil
}

// BuildManifestBusinessSeedRows preserves any trusted legacy seed rows and
// fills only uncovered business objects with one Runtime-generated baseline
// row. Model-authored manifests no longer need to carry per-table fixtures.
func BuildManifestBusinessSeedRows(manifest manifestmodel.ManifestSchema) ([]manifestBusinessSeedRow, error) {
	return BuildManifestBusinessSeedRowsWithReferenceResolver(context.Background(), manifest, "", nil)
}

// BuildManifestBusinessSeedRowsWithReferenceResolver is the managed Runtime
// startup path. It keeps manifest generation deterministic while requiring an
// external resolver to prove every generated Foundation reference against the
// active workspace before the row is encoded or persisted.
func BuildManifestBusinessSeedRowsWithReferenceResolver(ctx context.Context, manifest manifestmodel.ManifestSchema, workspaceID string, resolver BaselineReferenceResolver) ([]manifestBusinessSeedRow, error) {
	rows := ManifestBusinessSeedRowsFromManifest(manifest)
	generated, err := generateManifestBusinessSeedRows(ctx, manifest, rows, strings.TrimSpace(workspaceID), resolver)
	if err != nil {
		return nil, err
	}
	return append(rows, generated...), nil
}

// SyncManifestBusinessSeedsWithReferenceResolver checks persisted object
// coverage before generating missing baselines. A restart must not need to
// re-resolve external Identity references for an object whose baseline already
// exists, while a partially initialized database still receives baselines for
// every uncovered object and can reference the first persisted target record.
func SyncManifestBusinessSeedsWithReferenceResolver(ctx context.Context, records recordrepository.RecordBusinessSeedRepository, manifest manifestmodel.ManifestSchema, workspaceID string, resolver BaselineReferenceResolver) error {
	if records == nil {
		return nil
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if len(workspaceID) == 0 {
		return fmt.Errorf("sync generated business seeds: workspace authority is required")
	}
	rows := ManifestBusinessSeedRowsFromManifest(manifest)
	covered := map[string]bool{}
	for _, row := range rows {
		covered[strings.TrimSpace(row.ObjectKey)] = true
	}
	probeRows := append([]manifestBusinessSeedRow(nil), rows...)
	for _, object := range manifest.Objects {
		objectKey := strings.TrimSpace(object.Key)
		if objectKey == "" || covered[objectKey] {
			continue
		}
		probeRows = append(probeRows, manifestBusinessSeedRow{Key: runtimeGeneratedBusinessSeedKey(objectKey), ObjectKey: objectKey})
	}
	targets, err := manifestBusinessSeedTargetStates(ctx, records, workspaceID, manifestBusinessSeedObjects(manifest), probeRows)
	if err != nil {
		return err
	}
	for _, row := range probeRows {
		objectKey := strings.TrimSpace(row.ObjectKey)
		if covered[objectKey] || !targets[objectKey].HasRecords {
			continue
		}
		rows = append(rows, row)
		covered[objectKey] = true
	}
	generated, err := generateManifestBusinessSeedRows(ctx, manifest, rows, workspaceID, resolver)
	if err != nil {
		return err
	}
	return SyncManifestBusinessSeeds(ctx, records, manifest, append(rows, generated...))
}

func ManifestBusinessSeedRowsFromManifest(manifest manifestmodel.ManifestSchema) []manifestBusinessSeedRow {
	rows := []manifestBusinessSeedRow{}
	seen := map[string]struct{}{}
	for index, seed := range manifest.SeedRecords {
		objectKey := strings.TrimSpace(seed.ObjectKey)
		if objectKey == "" {
			continue
		}
		data := map[string]any{}
		for key, value := range seed.Data {
			data[key] = value
		}
		seedKey := strings.TrimSpace(fmt.Sprint(data["__seed_key"]))
		if seedKey == "" || seedKey == "<nil>" {
			seedKey = fmt.Sprintf("%s_seed_%03d", objectKey, index+1)
			data["__seed_key"] = seedKey
		}
		if _, ok := seen[seedKey]; ok {
			continue
		}
		seen[seedKey] = struct{}{}
		raw, err := json.Marshal(data)
		if err != nil {
			continue
		}
		rows = append(rows, manifestBusinessSeedRow{
			Key:         seedKey,
			ObjectKey:   objectKey,
			DataJSON:    string(raw),
			OwnerUserID: strings.TrimSpace(seed.OwnerUserID),
			OwnerOrgID:  strings.TrimSpace(seed.OwnerOrgID),
			SourceKind:  valueOrDefault(strings.TrimSpace(seed.SourceKind), "template"),
			SourceID:    valueOrDefault(strings.TrimSpace(seed.SourceID), manifest.TemplateID),
		})
	}
	return rows
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func manifestBusinessSeedRunID() string {
	if runID := strings.TrimSpace(os.Getenv("SEED_RUN_ID")); runID != "" {
		return runID
	}
	return time.Now().UTC().Format("20060102150405")
}

func manifestBusinessSeedObjects(manifest manifestmodel.ManifestSchema) map[string]definitionmodel.ObjectSchema {
	out := map[string]definitionmodel.ObjectSchema{}
	for _, object := range manifest.Objects {
		if strings.TrimSpace(object.Key) != "" {
			out[object.Key] = object
		}
	}
	return out
}

type manifestBusinessSeedTargetState struct {
	HasRecords    bool
	FirstRecordID string
}

func manifestBusinessSeedTargetStates(ctx context.Context, records recordrepository.RecordBusinessSeedRepository, workspaceID string, objectByKey map[string]definitionmodel.ObjectSchema, rows []manifestBusinessSeedRow) (map[string]manifestBusinessSeedTargetState, error) {
	states := map[string]manifestBusinessSeedTargetState{}
	checked := map[string]bool{}
	for _, row := range rows {
		if checked[row.ObjectKey] {
			continue
		}
		checked[row.ObjectKey] = true
		object, ok := objectByKey[row.ObjectKey]
		if !ok {
			continue
		}
		page, err := records.ListRecords(ctx, workspaceID, object, recordmodel.RecordListQuery{
			Page: 1, PageSize: 1, SkipTotal: true, AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, Sort: []recordmodel.RecordSortRule{{Field: "id", Direction: "asc"}},
		})
		if err != nil {
			return nil, fmt.Errorf("check domain seed target %s: %w", row.ObjectKey, err)
		}
		state := manifestBusinessSeedTargetState{HasRecords: len(page.Items) > 0}
		if len(page.Items) > 0 {
			state.FirstRecordID = strings.TrimSpace(page.Items[0].ID)
		}
		states[row.ObjectKey] = state
	}
	return states, nil
}

func orderManifestBusinessSeedRows(rows []manifestBusinessSeedRow) ([]manifestBusinessSeedRow, error) {
	byKey := map[string]manifestBusinessSeedRow{}
	for _, row := range rows {
		key := strings.TrimSpace(row.Key)
		if key == "" {
			continue
		}
		if _, exists := byKey[key]; exists {
			return nil, fmt.Errorf("duplicate domain seed key: %s", key)
		}
		byKey[key] = row
	}
	ordered := []manifestBusinessSeedRow{}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(manifestBusinessSeedRow) error
	visit = func(row manifestBusinessSeedRow) error {
		key := strings.TrimSpace(row.Key)
		if key == "" {
			ordered = append(ordered, row)
			return nil
		}
		if visited[key] {
			return nil
		}
		if visiting[key] {
			// IDs are derived from object and seed keys before any insert. A
			// cyclic relation is therefore safe: every edge already resolves
			// to its final deterministic record ID.
			return nil
		}
		visiting[key] = true
		for _, ref := range collectManifestBusinessSeedRefs(row.DataJSON) {
			dependency, ok := byKey[ref]
			if !ok {
				return fmt.Errorf("domain seed reference not found: %s", ref)
			}
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visiting[key] = false
		visited[key] = true
		ordered = append(ordered, row)
		return nil
	}
	for _, row := range rows {
		if err := visit(row); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

func collectManifestBusinessSeedRefs(dataJSON string) []string {
	var value any
	if strings.TrimSpace(dataJSON) == "" || json.Unmarshal([]byte(dataJSON), &value) != nil {
		return nil
	}
	refs := map[string]bool{}
	collectManifestBusinessSeedRefsFromValue(value, refs)
	out := make([]string, 0, len(refs))
	for ref := range refs {
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}

func collectManifestBusinessSeedRefsFromValue(value any, refs map[string]bool) {
	switch typed := value.(type) {
	case string:
		if ref := strings.TrimPrefix(typed, "$record:"); ref != typed {
			if ref = strings.TrimSpace(ref); ref != "" {
				refs[ref] = true
			}
		}
	case []any:
		for _, item := range typed {
			collectManifestBusinessSeedRefsFromValue(item, refs)
		}
	case map[string]any:
		for _, item := range typed {
			collectManifestBusinessSeedRefsFromValue(item, refs)
		}
	}
}

func manifestBusinessSeedIDs(rows []manifestBusinessSeedRow) map[string]string {
	out := map[string]string{}
	for _, row := range rows {
		key := strings.TrimSpace(row.Key)
		if key != "" {
			out[key] = manifestBusinessSeedRecordID(row.ObjectKey, key)
		}
	}
	return out
}

func manifestBusinessSeedRecordID(objectKey string, key string) string {
	id := strings.TrimSpace(objectKey) + "_" + strings.TrimSpace(key)
	id = strings.NewReplacer(" ", "_", "-", "_", ".", "_", "/", "_", ":", "_").Replace(id)
	id = strings.Trim(id, "_")
	if id == "" {
		return "seed_record"
	}
	return id
}

func resolveManifestBusinessSeedRefs(value any, idsByKey map[string]string) any {
	switch typed := value.(type) {
	case string:
		if ref := strings.TrimPrefix(typed, "$record:"); ref != typed {
			ref = strings.TrimSpace(ref)
			if id := idsByKey[ref]; id != "" {
				return id
			}
		}
		return typed
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, resolveManifestBusinessSeedRefs(item, idsByKey))
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for key, item := range typed {
			out[key] = resolveManifestBusinessSeedRefs(item, idsByKey)
		}
		return out
	default:
		return typed
	}
}

func resolveManifestBusinessSeedPlaceholders(value any, runID string) any {
	switch typed := value.(type) {
	case string:
		if runID != "" {
			return strings.ReplaceAll(typed, "${RUN_ID}", runID)
		}
		return typed
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, resolveManifestBusinessSeedPlaceholders(item, runID))
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for key, item := range typed {
			out[key] = resolveManifestBusinessSeedPlaceholders(item, runID)
		}
		return out
	default:
		return typed
	}
}

func filterManifestBusinessSeedData(object definitionmodel.ObjectSchema, data any) map[string]any {
	source, ok := data.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	fieldKeys := map[string]bool{}
	for _, field := range object.Fields {
		fieldKeys[field.Key] = true
	}
	out := map[string]any{}
	for key, value := range source {
		if key == "__seed_key" || !fieldKeys[key] || recordvalidation.RecordIsEmptyValue(value) {
			continue
		}
		out[key] = value
	}
	return out
}
