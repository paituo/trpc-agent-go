//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sqlitevec

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

const testDimension = 4

// newTestStore creates an in-memory sqlitevec store for testing.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := New(
		WithDSN(":memory:"),
		WithIndexDimension(testDimension),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func testEmbedding(vals ...float64) []float64 {
	emb := make([]float64, testDimension)
	for i, v := range vals {
		if i < testDimension {
			emb[i] = v
		}
	}
	return emb
}

// ---------- Add / Get ----------

func TestAddAndGet(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	doc := &document.Document{
		ID:      "doc-1",
		Name:    "Test Doc",
		Content: "Hello world",
		Metadata: map[string]any{
			"category": "tech",
			"score":    42,
		},
	}
	emb := testEmbedding(1, 0, 0, 0)

	require.NoError(t, store.Add(ctx, doc, emb))

	got, gotEmb, err := store.Get(ctx, "doc-1")
	require.NoError(t, err)
	assert.Equal(t, "doc-1", got.ID)
	assert.Equal(t, "Test Doc", got.Name)
	assert.Equal(t, "Hello world", got.Content)
	assert.NotNil(t, got.Metadata)
	assert.Equal(t, "tech", got.Metadata["category"])
	assert.False(t, got.CreatedAt.IsZero())
	assert.False(t, got.UpdatedAt.IsZero())
	assert.Equal(t, testDimension, len(gotEmb))
}

func TestGet_PrefersMetadataTable(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	doc := &document.Document{
		ID:      "doc-1",
		Content: "Hello world",
		Metadata: map[string]any{
			"category": "tech",
		},
	}
	require.NoError(t, store.Add(ctx, doc, testEmbedding(1, 0, 0, 0)))

	_, err := store.db.ExecContext(ctx, fmt.Sprintf(`INSERT OR REPLACE INTO %s
		(doc_id, key, value_ordinal, value_type, value_text, value_num, value_bool, value_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, store.opts.metadataTableName),
		"doc-1", "category", 0, metadataValueTypeText, "updated", nil, nil, `"updated"`,
	)
	require.NoError(t, err)

	got, _, err := store.Get(ctx, "doc-1")
	require.NoError(t, err)
	assert.Equal(t, "updated", got.Metadata["category"])
}

func TestGet_PreservesSingleElementMetadataArray(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &document.Document{
		ID:      "doc-1",
		Content: "c",
		Metadata: map[string]any{
			"tags": []string{"sqlite"},
		},
	}, testEmbedding(1)))

	got, _, err := store.Get(ctx, "doc-1")
	require.NoError(t, err)
	require.Contains(t, got.Metadata, "tags")
	assert.Equal(t, []any{"sqlite"}, got.Metadata["tags"])
}

func TestAdd_NilDoc(t *testing.T) {
	store := newTestStore(t)
	err := store.Add(context.Background(), nil, testEmbedding(1))
	assert.ErrorIs(t, err, errDocNil)
}

func TestAdd_EmptyID(t *testing.T) {
	store := newTestStore(t)
	err := store.Add(context.Background(), &document.Document{}, testEmbedding(1))
	assert.ErrorIs(t, err, errDocIDEmpty)
}

func TestAdd_EmptyEmbedding(t *testing.T) {
	store := newTestStore(t)
	err := store.Add(context.Background(), &document.Document{ID: "x"}, nil)
	assert.ErrorIs(t, err, errEmbeddingEmpty)
}

func TestGet_NotFound(t *testing.T) {
	store := newTestStore(t)
	_, _, err := store.Get(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ---------- Update ----------

func TestUpdate(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	doc := &document.Document{
		ID:      "doc-1",
		Name:    "Original",
		Content: "v1",
	}
	require.NoError(t, store.Add(ctx, doc, testEmbedding(1, 0, 0, 0)))

	updated := &document.Document{
		ID:      "doc-1",
		Name:    "Updated",
		Content: "v2",
		Metadata: map[string]any{
			"version": "2",
		},
	}
	require.NoError(t, store.Update(ctx, updated, testEmbedding(0, 1, 0, 0)))

	got, _, err := store.Get(ctx, "doc-1")
	require.NoError(t, err)
	assert.Equal(t, "Updated", got.Name)
	assert.Equal(t, "v2", got.Content)
	assert.Equal(t, "2", got.Metadata["version"])
	// CreatedAt should be preserved.
	assert.False(t, got.CreatedAt.IsZero())
}

func TestUpdate_NotFound(t *testing.T) {
	store := newTestStore(t)
	err := store.Update(context.Background(), &document.Document{ID: "nope"}, testEmbedding(1))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ---------- Delete ----------

func TestDelete(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	doc := &document.Document{ID: "doc-1", Content: "c"}
	require.NoError(t, store.Add(ctx, doc, testEmbedding(1)))

	require.NoError(t, store.Delete(ctx, "doc-1"))

	_, _, err := store.Get(ctx, "doc-1")
	assert.Error(t, err)
}

func TestDelete_NotFound(t *testing.T) {
	store := newTestStore(t)
	err := store.Delete(context.Background(), "missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ---------- SearchModeVector ----------

func TestSearchModeVector(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Add two documents with different embeddings.
	require.NoError(t, store.Add(ctx, &document.Document{
		ID: "a", Name: "Alpha", Content: "alpha content",
	}, testEmbedding(1, 0, 0, 0)))

	require.NoError(t, store.Add(ctx, &document.Document{
		ID: "b", Name: "Beta", Content: "beta content",
	}, testEmbedding(0, 1, 0, 0)))

	result, err := store.Search(ctx, &vectorstore.SearchQuery{
		Vector:     testEmbedding(1, 0, 0, 0),
		Limit:      10,
		SearchMode: vectorstore.SearchModeVector,
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.Results)
	// The first result should be the most similar.
	assert.Equal(t, "a", result.Results[0].Document.ID)
	assert.Greater(t, result.Results[0].Score, 0.0)
	assert.LessOrEqual(t, result.Results[0].Score, 1.0)
}

func TestSearchModeVector_EmptyVector(t *testing.T) {
	store := newTestStore(t)
	_, err := store.Search(context.Background(), &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeVector,
	})
	assert.Error(t, err)
}

// ---------- SearchModeFilter ----------

func TestSearchModeFilter(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &document.Document{
		ID: "a", Name: "Alpha", Content: "c", Metadata: map[string]any{"cat": "tech"},
	}, testEmbedding(1)))

	require.NoError(t, store.Add(ctx, &document.Document{
		ID: "b", Name: "Beta", Content: "c", Metadata: map[string]any{"cat": "science"},
	}, testEmbedding(0, 1)))

	result, err := store.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeFilter,
		Filter: &vectorstore.SearchFilter{
			Metadata: map[string]any{"cat": "tech"},
		},
		Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	assert.Equal(t, "a", result.Results[0].Document.ID)
	assert.Equal(t, 1.0, result.Results[0].Score)
}

func TestSearchModeFilter_MetadataPromotedFieldsNowIndexedInMetadataTable(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &document.Document{
		ID:      "a",
		Content: "c",
		Metadata: map[string]any{
			"source_name": "docs",
			"chunk_index": 3,
		},
	}, testEmbedding(1)))

	require.NoError(t, store.Add(ctx, &document.Document{
		ID:      "b",
		Content: "c",
		Metadata: map[string]any{
			"source_name": "wiki",
			"chunk_index": 1,
		},
	}, testEmbedding(0, 1)))

	result, err := store.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeFilter,
		Filter: &vectorstore.SearchFilter{
			Metadata: map[string]any{"source_name": "docs"},
		},
		Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	assert.Equal(t, "a", result.Results[0].Document.ID)
}

func TestSearchModeFilter_MetadataArrayElementMatch(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &document.Document{
		ID:      "a",
		Content: "c",
		Metadata: map[string]any{
			"tags": []string{"rag", "sqlite"},
		},
	}, testEmbedding(1)))

	require.NoError(t, store.Add(ctx, &document.Document{
		ID:      "b",
		Content: "c",
		Metadata: map[string]any{
			"tags": []string{"agent", "golang"},
		},
	}, testEmbedding(0, 1)))

	result, err := store.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeFilter,
		Filter: &vectorstore.SearchFilter{
			Metadata: map[string]any{"tags": "sqlite"},
		},
		Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	assert.Equal(t, "a", result.Results[0].Document.ID)
}

func TestSearchModeFilter_InvalidUniversalFilterConditionReturnsError(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &document.Document{
		ID: "a", Content: "c",
	}, testEmbedding(1)))

	_, err := store.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeFilter,
		Filter: &vectorstore.SearchFilter{
			FilterCondition: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorBetween,
				Field:    "metadata.score",
				Value:    1,
			},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "between operator")
}

func TestSearchModeFilter_MetadataReservedFieldDoesNotHitTopLevelColumn(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &document.Document{
		ID:      "doc-top",
		Content: "c",
		Metadata: map[string]any{
			"id": "meta-a",
		},
	}, testEmbedding(1)))
	require.NoError(t, store.Add(ctx, &document.Document{
		ID:      "meta-a",
		Content: "c",
		Metadata: map[string]any{
			"id": "something-else",
		},
	}, testEmbedding(0, 1)))

	result, err := store.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeFilter,
		Filter: &vectorstore.SearchFilter{
			FilterCondition: &searchfilter.UniversalFilterCondition{
				Field:    "metadata.id",
				Operator: searchfilter.OperatorEqual,
				Value:    "meta-a",
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	assert.Equal(t, "doc-top", result.Results[0].Document.ID)
}

func TestSearch_UsesMetadataTableValues(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &document.Document{
		ID:      "doc-1",
		Content: "c",
		Metadata: map[string]any{
			"category": "tech",
		},
	}, testEmbedding(1)))

	_, err := store.db.ExecContext(ctx, fmt.Sprintf(`INSERT OR REPLACE INTO %s
		(doc_id, key, value_ordinal, value_type, value_text, value_num, value_bool, value_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, store.opts.metadataTableName),
		"doc-1", "category", 0, metadataValueTypeText, "updated", nil, nil, `"updated"`,
	)
	require.NoError(t, err)

	result, err := store.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeFilter,
		Filter: &vectorstore.SearchFilter{
			IDs: []string{"doc-1"},
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	assert.Equal(t, "updated", result.Results[0].Document.Metadata["category"])
}

// ---------- Unsupported modes ----------

func TestSearchModeKeyword_Unsupported(t *testing.T) {
	store := newTestStore(t)
	_, err := store.Search(context.Background(), &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeKeyword,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "enableFTS")
}

func TestSearchModeHybrid_Unsupported(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &document.Document{
		ID: "a", Content: "alpha",
	}, testEmbedding(1, 0, 0, 0)))
	require.NoError(t, store.Add(ctx, &document.Document{
		ID: "b", Content: "beta",
	}, testEmbedding(0, 1, 0, 0)))

	result, err := store.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeHybrid,
		Vector:     testEmbedding(1, 0, 0, 0),
		Limit:      10,
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.Results)
	assert.Equal(t, "a", result.Results[0].Document.ID)
}

func TestSearchModeHybrid_EmptyVector(t *testing.T) {
	store := newTestStore(t)
	_, err := store.Search(context.Background(), &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeHybrid,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "query vector cannot be empty")
}

// ---------- Count ----------

func TestCount(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &document.Document{
		ID: "a", Content: "c", Metadata: map[string]any{"cat": "tech"},
	}, testEmbedding(1)))
	require.NoError(t, store.Add(ctx, &document.Document{
		ID: "b", Content: "c", Metadata: map[string]any{"cat": "science"},
	}, testEmbedding(0, 1)))

	count, err := store.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	count, err = store.Count(ctx, vectorstore.WithCountFilter(map[string]any{"cat": "tech"}))
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// ---------- GetMetadata ----------

func TestGetMetadata(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &document.Document{
		ID: "a", Content: "c", Metadata: map[string]any{"lang": "go"},
	}, testEmbedding(1)))

	meta, err := store.GetMetadata(ctx, vectorstore.WithGetMetadataIDs([]string{"a"}))
	require.NoError(t, err)
	require.Contains(t, meta, "a")
	assert.Equal(t, "go", meta["a"].Metadata["lang"])
}

func TestGetMetadata_UsesMetadataTableValues(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &document.Document{
		ID:       "a",
		Content:  "c",
		Metadata: map[string]any{"lang": "go"},
	}, testEmbedding(1)))

	_, err := store.db.ExecContext(ctx, fmt.Sprintf(`INSERT OR REPLACE INTO %s
		(doc_id, key, value_ordinal, value_type, value_text, value_num, value_bool, value_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, store.opts.metadataTableName),
		"a", "lang", 0, metadataValueTypeText, "rust", nil, nil, `"rust"`,
	)
	require.NoError(t, err)

	meta, err := store.GetMetadata(ctx, vectorstore.WithGetMetadataIDs([]string{"a"}))
	require.NoError(t, err)
	require.Contains(t, meta, "a")
	assert.Equal(t, "rust", meta["a"].Metadata["lang"])
}

func TestGetMetadata_Pagination(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		require.NoError(t, store.Add(ctx, &document.Document{
			ID:      fmt.Sprintf("doc-%d", i),
			Content: "c",
		}, testEmbedding(1)))
	}

	meta, err := store.GetMetadata(ctx,
		vectorstore.WithGetMetadataLimit(2),
		vectorstore.WithGetMetadataOffset(0),
	)
	require.NoError(t, err)
	assert.Len(t, meta, 2)
}

func TestGetMetadata_PaginationUsesStableOrdering(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		require.NoError(t, store.Add(ctx, &document.Document{
			ID:      fmt.Sprintf("doc-%d", i),
			Content: "c",
		}, testEmbedding(1)))
	}

	page1, err := store.GetMetadata(ctx,
		vectorstore.WithGetMetadataLimit(1),
		vectorstore.WithGetMetadataOffset(0),
	)
	require.NoError(t, err)
	page2, err := store.GetMetadata(ctx,
		vectorstore.WithGetMetadataLimit(1),
		vectorstore.WithGetMetadataOffset(1),
	)
	require.NoError(t, err)

	require.Len(t, page1, 1)
	require.Len(t, page2, 1)

	var ids1 []string
	for id := range page1 {
		ids1 = append(ids1, id)
	}
	var ids2 []string
	for id := range page2 {
		ids2 = append(ids2, id)
	}
	assert.NotEqual(t, ids1[0], ids2[0])
}

// ---------- DeleteByFilter ----------

func TestDeleteByFilter(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &document.Document{
		ID: "a", Content: "c", Metadata: map[string]any{"env": "prod"},
	}, testEmbedding(1)))
	require.NoError(t, store.Add(ctx, &document.Document{
		ID: "b", Content: "c", Metadata: map[string]any{"env": "staging"},
	}, testEmbedding(0, 1)))

	err := store.DeleteByFilter(ctx, vectorstore.WithDeleteDocumentIDs([]string{"a"}))
	require.NoError(t, err)

	count, err := store.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestDeleteByFilter_All(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &document.Document{ID: "a", Content: "c"}, testEmbedding(1)))
	require.NoError(t, store.Add(ctx, &document.Document{ID: "b", Content: "c"}, testEmbedding(0, 1)))

	err := store.DeleteByFilter(ctx, vectorstore.WithDeleteAll(true))
	require.NoError(t, err)

	count, err := store.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

// ---------- UpdateByFilter ----------

func TestUpdateByFilter(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &document.Document{
		ID: "a", Name: "Old", Content: "c",
	}, testEmbedding(1)))

	updated, err := store.UpdateByFilter(ctx,
		vectorstore.WithUpdateByFilterDocumentIDs([]string{"a"}),
		vectorstore.WithUpdateByFilterUpdates(map[string]any{
			"name": "New",
		}),
	)
	require.NoError(t, err)
	assert.Equal(t, int64(1), updated)

	doc, _, err := store.Get(ctx, "a")
	require.NoError(t, err)
	assert.Equal(t, "New", doc.Name)
}

func TestUpdateByFilter_ContentWithoutEmbedding(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &document.Document{
		ID: "a", Content: "old",
	}, testEmbedding(1)))

	_, err := store.UpdateByFilter(ctx,
		vectorstore.WithUpdateByFilterDocumentIDs([]string{"a"}),
		vectorstore.WithUpdateByFilterUpdates(map[string]any{
			"content": "new",
		}),
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "embedding")
}

func TestUpdateByFilter_InvalidConditionReturnsError(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &document.Document{
		ID: "a", Name: "Old", Content: "c",
	}, testEmbedding(1)))

	updated, err := store.UpdateByFilter(ctx,
		vectorstore.WithUpdateByFilterCondition(&searchfilter.UniversalFilterCondition{
			Field:    "metadata.score",
			Operator: searchfilter.OperatorBetween,
			Value:    1,
		}),
		vectorstore.WithUpdateByFilterUpdates(map[string]any{
			"name": "New",
		}),
	)
	require.Error(t, err)
	assert.Equal(t, int64(0), updated)

	doc, _, getErr := store.Get(ctx, "a")
	require.NoError(t, getErr)
	assert.Equal(t, "Old", doc.Name)
}

// ---------- Filter condition tests ----------

func TestSearchWithUniversalFilterCondition(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Add(ctx, &document.Document{
		ID: "a", Content: "c", Metadata: map[string]any{"score": 80.0, "lang": "go"},
	}, testEmbedding(1)))
	require.NoError(t, store.Add(ctx, &document.Document{
		ID: "b", Content: "c", Metadata: map[string]any{"score": 50.0, "lang": "py"},
	}, testEmbedding(0, 1)))

	result, err := store.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeFilter,
		Filter: &vectorstore.SearchFilter{
			FilterCondition: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorAnd,
				Value: []*searchfilter.UniversalFilterCondition{
					{
						Field:    "metadata.score",
						Operator: searchfilter.OperatorGreaterThanOrEqual,
						Value:    70.0,
					},
					{
						Field:    "metadata.lang",
						Operator: searchfilter.OperatorEqual,
						Value:    "go",
					},
				},
			},
		},
		Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	assert.Equal(t, "a", result.Results[0].Document.ID)
}

// ---------- Multi-table consistency ----------

func TestMultiTableConsistency(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	doc := &document.Document{
		ID: "doc-1", Content: "c",
		Metadata: map[string]any{"k1": "v1", "k2": 42},
	}
	require.NoError(t, store.Add(ctx, doc, testEmbedding(1)))

	// Update with different metadata.
	doc.Metadata = map[string]any{"k1": "v1-updated", "k3": true}
	require.NoError(t, store.Update(ctx, doc, testEmbedding(1)))

	got, _, err := store.Get(ctx, "doc-1")
	require.NoError(t, err)
	assert.Equal(t, "v1-updated", got.Metadata["k1"])
	assert.Nil(t, got.Metadata["k2"]) // k2 should be gone.
	assert.Equal(t, true, got.Metadata["k3"])
}

// ---------- Close ----------

func TestClose(t *testing.T) {
	store, err := New(WithDSN(":memory:"), WithIndexDimension(testDimension))
	require.NoError(t, err)

	require.NoError(t, store.Close())
	assert.Error(t, store.db.Ping())
}

func TestIsSQLiteMemoryDSN(t *testing.T) {
	tests := []struct {
		dsn  string
		want bool
	}{
		{dsn: ":memory:", want: true},
		{dsn: "file::memory:?cache=shared", want: true},
		{dsn: "file:memdb1?mode=memory&cache=shared", want: true},
		{dsn: "file:/tmp/test.db?_busy_timeout=5000", want: false},
		{dsn: "/tmp/test.db", want: false},
		{dsn: "", want: false},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, isSQLiteMemoryDSN(tt.dsn), tt.dsn)
	}
}

// ---------- FTS / Hybrid Search ----------

func TestSearchModeKeyword_WithFTS(t *testing.T) {
	s := newTestStoreWithFTS(t)

	docs := []struct {
		id      string
		name    string
		content string
	}{
		{"1", "Go语言入门", "Go语言是一种静态类型的编译型语言"},
		{"2", "Python教程", "Python是一种动态类型的解释型语言"},
		{"3", "Go并发编程", "Go语言的goroutine和channel支持高效并发"},
	}
	for _, d := range docs {
		doc := &document.Document{ID: d.id, Name: d.name, Content: d.content}
		if err := s.Add(context.Background(), doc, testEmbedding(0.1, 0.2, 0.3, 0.4)); err != nil {
			t.Fatalf("Add %s: %v", d.id, err)
		}
	}

	result, err := s.Search(context.Background(), &vectorstore.SearchQuery{
		Query:      "Go语言",
		SearchMode: vectorstore.SearchModeKeyword,
	})
	if err != nil {
		t.Fatalf("Search keyword: %v", err)
	}
	if len(result.Results) == 0 {
		t.Fatal("expected keyword search results, got none")
	}
	foundGo := false
	for _, r := range result.Results {
		if strings.Contains(r.Document.Name, "Go") || strings.Contains(r.Document.Content, "Go") {
			foundGo = true
		}
	}
	if !foundGo {
		t.Error("expected Go-related results in keyword search")
	}
}

func TestSearchModeKeyword_RequiresFTS(t *testing.T) {
	s := newTestStore(t) // FTS not enabled

	_, err := s.Search(context.Background(), &vectorstore.SearchQuery{
		Query:      "test",
		SearchMode: vectorstore.SearchModeKeyword,
	})
	if err == nil {
		t.Fatal("expected error for keyword search without FTS")
	}
}

func TestSearchModeKeyword_EmptyQuery(t *testing.T) {
	s := newTestStoreWithFTS(t)

	_, err := s.Search(context.Background(), &vectorstore.SearchQuery{
		Query:      "",
		SearchMode: vectorstore.SearchModeKeyword,
	})
	if err == nil {
		t.Fatal("expected error for empty keyword query")
	}
}

func TestSearchModeHybrid_WithFTS(t *testing.T) {
	s := newTestStoreWithFTS(t)

	docs := []struct {
		id      string
		name    string
		content string
	}{
		{"1", "Go语言入门", "Go语言是一种静态类型的编译型语言"},
		{"2", "Python教程", "Python是一种动态类型的解释型语言"},
		{"3", "Go并发编程", "Go语言的goroutine和channel支持高效并发"},
	}
	for _, d := range docs {
		doc := &document.Document{ID: d.id, Name: d.name, Content: d.content}
		if err := s.Add(context.Background(), doc, testEmbedding(0.1, 0.2, 0.3, 0.4)); err != nil {
			t.Fatalf("Add %s: %v", d.id, err)
		}
	}

	result, err := s.Search(context.Background(), &vectorstore.SearchQuery{
		Query:      "Go语言",
		Vector:     testEmbedding(0.1, 0.2, 0.3, 0.4),
		SearchMode: vectorstore.SearchModeHybrid,
	})
	if err != nil {
		t.Fatalf("Search hybrid: %v", err)
	}
	if len(result.Results) == 0 {
		t.Fatal("expected hybrid search results, got none")
	}
	for _, r := range result.Results {
		if r.Document.Metadata == nil {
			t.Error("expected metadata with scores")
			continue
		}
		if _, ok := r.Document.Metadata[source.MetadataDenseScore]; !ok {
			t.Errorf("missing %s in metadata for doc %s", source.MetadataDenseScore, r.Document.ID)
		}
		if _, ok := r.Document.Metadata[source.MetadataSparseScore]; !ok {
			t.Errorf("missing %s in metadata for doc %s", source.MetadataSparseScore, r.Document.ID)
		}
	}
}

func TestSearchModeHybrid_WithoutFTS_FallsBackToVector(t *testing.T) {
	s := newTestStore(t) // FTS not enabled

	doc := &document.Document{ID: "1", Content: "test content"}
	if err := s.Add(context.Background(), doc, testEmbedding(0.1, 0.2, 0.3, 0.4)); err != nil {
		t.Fatalf("Add: %v", err)
	}

	result, err := s.Search(context.Background(), &vectorstore.SearchQuery{
		Query:      "test",
		Vector:     testEmbedding(0.1, 0.2, 0.3, 0.4),
		SearchMode: vectorstore.SearchModeHybrid,
	})
	if err != nil {
		t.Fatalf("Search hybrid fallback: %v", err)
	}
	if len(result.Results) == 0 {
		t.Fatal("expected vector fallback results")
	}
}

func TestFTS_SyncOnAddUpdateDelete(t *testing.T) {
	s := newTestStoreWithFTS(t)

	// Add
	doc := &document.Document{ID: "1", Content: "机器学习是人工智能的分支"}
	if err := s.Add(context.Background(), doc, testEmbedding(0.1, 0.2, 0.3, 0.4)); err != nil {
		t.Fatalf("Add: %v", err)
	}

	result, err := s.Search(context.Background(), &vectorstore.SearchQuery{
		Query:      "机器学习",
		SearchMode: vectorstore.SearchModeKeyword,
	})
	if err != nil {
		t.Fatalf("Search after add: %v", err)
	}
	if len(result.Results) != 1 {
		t.Fatalf("expected 1 result after add, got %d", len(result.Results))
	}

	// Update
	doc.Content = "深度学习是机器学习的子领域"
	if err := s.Update(context.Background(), doc, testEmbedding(0.1, 0.2, 0.3, 0.4)); err != nil {
		t.Fatalf("Update: %v", err)
	}

	result, err = s.Search(context.Background(), &vectorstore.SearchQuery{
		Query:      "深度学习",
		SearchMode: vectorstore.SearchModeKeyword,
	})
	if err != nil {
		t.Fatalf("Search after update: %v", err)
	}
	if len(result.Results) != 1 {
		t.Fatalf("expected 1 result after update, got %d", len(result.Results))
	}

	// Delete
	if err := s.Delete(context.Background(), "1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	result, err = s.Search(context.Background(), &vectorstore.SearchQuery{
		Query:      "深度学习",
		SearchMode: vectorstore.SearchModeKeyword,
	})
	if err != nil {
		t.Fatalf("Search after delete: %v", err)
	}
	if len(result.Results) != 0 {
		t.Fatalf("expected 0 results after delete, got %d", len(result.Results))
	}
}
