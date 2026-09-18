// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
// Licensed under the Apache License, Version 2.0.

package mongodb

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestMongoDB40Integration(t *testing.T) {
	if os.Getenv("VEGA_TEST_MONGODB40") != "1" {
		t.Skip("set VEGA_TEST_MONGODB40=1 to run against MongoDB 4.0")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	builder := NewMongoDBConnector()
	builder.SetEnabled(true)
	instance, err := builder.New(interfaces.ConnectorConfig{
		"host": "127.0.0.1", "port": 27017, "database": "openbkn",
		"username": "openbkn", "password": "openbkn-mongo-test", "auth_source": "admin", "direct": true,
	})
	require.NoError(t, err)
	connector := instance.(*Connector)
	require.NoError(t, connector.Connect(ctx))
	t.Cleanup(func() { _ = connector.Close(context.Background()) })

	metadata, err := connector.GetMetadata(ctx)
	require.NoError(t, err)
	assert.Equal(t, "4.0.13", metadata["version"])

	collections, err := connector.ListIndexes(ctx)
	require.NoError(t, err)
	require.Len(t, collections, 1)
	assert.Equal(t, "customers", collections[0].Name)

	require.NoError(t, connector.GetIndexMeta(ctx, collections[0]))
	assert.Equal(t, "decimal", collections[0].Mapping["balance"].Type)
	assert.Equal(t, "string", collections[0].Mapping["address.city"].Type)

	result, err := connector.ExecuteQuery(ctx, "customers", nil, &interfaces.ResourceDataQueryParams{
		Paging: interfaces.PagingRequest{Offset: 0, Limit: 1},
		Sort:   []*interfaces.SortField{{Field: "age", Direction: "desc"}},
	})
	require.NoError(t, err)
	assert.EqualValues(t, 2, result.Total)
	require.Len(t, result.Entries, 1)
	assert.Equal(t, "Bob", result.Entries[0]["name"])
	assert.NotEmpty(t, result.Entries[0]["_id"])
}
