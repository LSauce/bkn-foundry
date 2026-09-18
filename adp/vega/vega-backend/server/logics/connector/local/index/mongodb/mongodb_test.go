// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
// Licensed under the Apache License, Version 2.0.

package mongodb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"vega-backend/interfaces"
)

func TestConnectorMetadataAndConfig(t *testing.T) {
	connector := NewMongoDBConnector()
	assert.Equal(t, interfaces.ConnectorTypeMongoDB, connector.GetType())
	assert.Equal(t, interfaces.ConnectorCategoryIndex, connector.GetCategory())
	assert.Contains(t, connector.GetSensitiveFields(), "password")
	assert.True(t, connector.GetFieldConfig()["database"].Required)

	_, err := connector.New(interfaces.ConnectorConfig{"host": "127.0.0.1", "port": 27017})
	require.Error(t, err)
	instance, err := connector.New(interfaces.ConnectorConfig{"host": "127.0.0.1", "port": 27017, "database": "openbkn"})
	require.NoError(t, err)
	assert.Equal(t, "admin", instance.(*Connector).config.AuthSource)
}

func TestConnectorMapType(t *testing.T) {
	c := &Connector{}
	tests := map[string]string{"objectId": interfaces.DataType_String, "bool": interfaces.DataType_Boolean, "long": interfaces.DataType_Integer, "decimal": interfaces.DataType_Decimal, "date": interfaces.DataType_Datetime, "object": interfaces.DataType_Json, "array": interfaces.DataType_Json}
	for native, expected := range tests {
		assert.Equal(t, expected, c.MapType(native), native)
	}
}

func TestNormalizeDocument(t *testing.T) {
	id := primitive.NewObjectID()
	got := normalizeDocument(bson.M{"_id": id, "nested": bson.M{"ok": true}})
	assert.Equal(t, id.Hex(), got["_id"])
	assert.Equal(t, true, got["nested"].(map[string]any)["ok"])
}
