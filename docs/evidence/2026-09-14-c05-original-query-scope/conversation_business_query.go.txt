package agenthost

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"slices"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
)

func (h *ConversationBusinessHost) QueryBusinessRecords(ctx context.Context, q agentsdk.ConversationBusinessQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationBusinessRecordPage, error) {
	return h.queryBusinessRecords(ctx, q, a, nil, "")
}

// required is a host-resolved relationship constraint, never model input.
func (h *ConversationBusinessHost) queryBusinessRecords(ctx context.Context, q agentsdk.ConversationBusinessQuery, a agentsdk.ConversationAuthority, required *recordmodel.RecordFilterExpression, relationScope string) (agentsdk.ConversationBusinessRecordPage, error) {
	out := agentsdk.ConversationBusinessRecordPage{ObjectKey: q.ObjectKey, Items: []agentsdk.ConversationBusinessRecord{}}
	if q.Page == 0 && q.Cursor == "" {
		q.Page = 1
	}
	if q.PageSize == 0 {
		q.PageSize = 20
	}
	if q.Page < 0 || q.Page > 1000000 || q.PageSize < 1 || q.PageSize > 25 || len(q.Filters) > 20 || len(q.Sort) > 5 || q.Page > 1 && q.Cursor == "" {
		return out, conversationBusinessError("bad_request")
	}
	object, p, err := h.object(ctx, q.ObjectKey, a)
	if err != nil {
		return out, err
	}
	fields, err := businessSelectFields(object, q.Fields)
	if err != nil {
		return out, err
	}
	query := recordmodel.RecordListQuery{Page: 1, PageSize: q.PageSize, SelectFields: append([]string(nil), fields...), StableNullsLast: true}
	// An empty field list means "all fields" to Runtime. Use the record ID
	// envelope when the user has no readable business fields.
	if len(fields) == 0 {
		query.SelectFields = []string{"id"}
	}
	definitions := map[string]agentsdk.ConversationBusinessField{}
	for _, field := range object.Fields {
		definitions[field.Key] = businessField(field, object.Key, p)
	}
	// Validate before Runtime's legacy normalizer, which drops invalid filters
	// and sort keys. A malformed model request must never become a broader read.
	query.FilterExpression, err = businessQueryFilterExpression(object, definitions, q.Filters, required)
	if err != nil {
		return out, conversationBusinessError("bad_request")
	}
	seen := map[string]bool{}
	for _, rule := range q.Sort {
		field, ok := definitions[rule.Field]
		if !ok || !field.Sortable || seen[rule.Field] || rule.Direction != "asc" && rule.Direction != "desc" {
			return out, conversationBusinessError("bad_request")
		}
		seen[rule.Field] = true
		query.Sort = append(query.Sort, recordmodel.RecordSortRule{Field: rule.Field, Direction: rule.Direction})
		if !slices.Contains(query.SelectFields, rule.Field) {
			query.SelectFields = append(query.SelectFields, rule.Field)
		}
	}
	query.Sort = append(query.Sort, recordmodel.RecordSortRule{Field: "id", Direction: "asc"})
	if q.Cursor != "" {
		cursor, err := h.decodeCursor(q, a, query.Sort, relationScope)
		if err != nil {
			return out, err
		}
		seek, err := businessCursorFilter(cursor, query.Sort)
		if err != nil {
			return out, err
		}
		if query.FilterExpression != nil {
			query.FilterExpression = businessLogical("and", []recordmodel.RecordFilterExpression{*query.FilterExpression, *seek})
		} else {
			query.FilterExpression = seek
		}
		query.FilterExpression, err = recordvalidation.RecordNormalizeFilterExpression(object, query.FilterExpression)
		if err != nil {
			return out, conversationBusinessError("bad_request")
		}
		q.Page = cursor.Page
		query.SkipTotal = true
	}
	page, err := h.records.ListRecords(ctx, object.Key, query, p)
	if err != nil {
		return out, conversationBusinessReadError(err)
	}
	out.Page, out.PageSize, out.HasNext = q.Page, page.PageSize, page.HasNext
	if q.Cursor == "" {
		total := int64(page.Total)
		out.Total = &total
	}
	for _, item := range page.Items {
		record, err := businessRecord(item, fields)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, record)
	}
	if out.HasNext && len(page.Items) > 0 {
		out.NextCursor, err = h.encodeCursor(q, a, query.Sort, page.Items[len(page.Items)-1], relationScope)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// Both real queries and saved-page scope proofs use the same field/operator,
// JSON and value normalization boundary. required remains host-resolved.
func businessQueryFilterExpression(object definitionmodel.ObjectSchema, definitions map[string]agentsdk.ConversationBusinessField, filters []agentsdk.ConversationBusinessFilter, required *recordmodel.RecordFilterExpression) (*recordmodel.RecordFilterExpression, error) {
	badRequest := func() (*recordmodel.RecordFilterExpression, error) {
		return nil, conversationBusinessError("bad_request")
	}
	if len(filters) > 20 {
		return badRequest()
	}
	nodes := make([]recordmodel.RecordFilterExpression, 0, len(filters)+1)
	for _, filter := range filters {
		field, ok := definitions[filter.Field]
		if !ok || !businessOperatorAllowed(field, filter.Operator) {
			return badRequest()
		}
		var value any
		decoder := json.NewDecoder(bytes.NewReader(filter.Value))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
			return badRequest()
		}
		node := recordmodel.RecordFilterExpression{Field: filter.Field, Operator: filter.Operator}
		switch filter.Operator {
		case "in", "not_in":
			values, ok := value.([]any)
			if !ok || len(values) == 0 || len(values) > 1000 {
				return badRequest()
			}
			node.Values = values
		case "is_null", "is_not_null":
			if value != nil {
				return badRequest()
			}
		default:
			node.Value = value
		}
		nodes = append(nodes, node)
	}
	if required != nil {
		nodes = append(nodes, *required)
	}
	var expression *recordmodel.RecordFilterExpression
	if len(nodes) == 1 {
		expression = &nodes[0]
	} else if len(nodes) > 1 {
		expression = &recordmodel.RecordFilterExpression{Operator: "and", Children: nodes}
	}
	normalized, err := recordvalidation.RecordNormalizeFilterExpression(object, expression)
	if err != nil {
		return badRequest()
	}
	return normalized, nil
}
