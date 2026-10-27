package ghost

import (
	"encoding/json"
	"errors"
	"sort"
)

// Ghost database exports store relations separately from posts. Resolve only
// public display fields later through cms.PublicPresentation; raw users and
// settings stay in the operator archive, including any credential material.
func withRelations(fields map[string]json.RawMessage, data map[string]json.RawMessage, sourceID string) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	for k, v := range fields {
		out[k] = v
	}
	for _, spec := range []struct{ field, join, collection, key string }{{"authors", "posts_authors", "users", "author_id"}, {"tags", "posts_tags", "tags", "tag_id"}} {
		if _, ok := out[spec.field]; ok {
			continue
		}
		var links []map[string]json.RawMessage
		var records []map[string]json.RawMessage
		if raw, ok := data[spec.join]; ok {
			if json.Unmarshal(raw, &links) != nil {
				return nil, errors.New("invalid relation collection")
			}
		}
		if raw, ok := data[spec.collection]; ok {
			if json.Unmarshal(raw, &records) != nil {
				return nil, errors.New("invalid relation records")
			}
		}
		index := map[string]map[string]json.RawMessage{}
		for _, record := range records {
			var id string
			_ = json.Unmarshal(record["id"], &id)
			if id != "" {
				index[id] = record
			}
		}
		sort.SliceStable(links, func(i, j int) bool {
			var a, b int
			_ = json.Unmarshal(links[i]["sort_order"], &a)
			_ = json.Unmarshal(links[j]["sort_order"], &b)
			return a < b
		})
		resolved := []map[string]json.RawMessage{}
		for _, link := range links {
			var post, id string
			_ = json.Unmarshal(link["post_id"], &post)
			if post != sourceID {
				continue
			}
			_ = json.Unmarshal(link[spec.key], &id)
			record, ok := index[id]
			if !ok {
				return nil, errors.New("unresolved relation")
			}
			resolved = append(resolved, record)
		}
		if spec.field == "authors" && len(resolved) == 0 {
			return nil, errors.New("source author relation missing")
		}
		out[spec.field], _ = json.Marshal(resolved)
	}
	return out, nil
}
