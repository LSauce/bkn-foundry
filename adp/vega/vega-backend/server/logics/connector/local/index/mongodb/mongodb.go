// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

// Package mongodb implements a MongoDB collection connector.
package mongodb

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mitchellh/mapstructure"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"vega-backend/interfaces"
)

const defaultSampleSize = int64(100)

type config struct {
	Host       string `mapstructure:"host"`
	Port       int    `mapstructure:"port"`
	Database   string `mapstructure:"database"`
	Username   string `mapstructure:"username"`
	Password   string `mapstructure:"password"`
	AuthSource string `mapstructure:"auth_source"`
	Direct     bool   `mapstructure:"direct"`
	TLS        bool   `mapstructure:"tls"`
}

// Connector exposes MongoDB collections as VEGA index resources.
type Connector struct {
	config  *config
	client  *mongo.Client
	enabled bool
}

func NewMongoDBConnector() interfaces.IndexConnector { return &Connector{} }

func (c *Connector) GetType() string              { return interfaces.ConnectorTypeMongoDB }
func (c *Connector) GetName() string              { return interfaces.ConnectorTypeMongoDB }
func (c *Connector) GetMode() string              { return interfaces.ConnectorModeLocal }
func (c *Connector) GetCategory() string          { return interfaces.ConnectorCategoryIndex }
func (c *Connector) GetEnabled() bool             { return c.enabled }
func (c *Connector) SetEnabled(v bool)            { c.enabled = v }
func (c *Connector) GetSensitiveFields() []string { return []string{"password"} }

func (c *Connector) GetFieldConfig() map[string]interfaces.ConnectorFieldConfig {
	return map[string]interfaces.ConnectorFieldConfig{
		"host":        {Name: "主机地址", Type: "string", Description: "MongoDB 服务器主机地址", Required: true},
		"port":        {Name: "端口号", Type: "integer", Description: "MongoDB 服务器端口", Required: true},
		"database":    {Name: "数据库", Type: "string", Description: "要发现的 MongoDB 数据库", Required: true},
		"username":    {Name: "用户名", Type: "string", Description: "认证用户名", Required: false},
		"password":    {Name: "密码", Type: "string", Description: "认证密码", Required: false, Encrypted: true},
		"auth_source": {Name: "认证库", Type: "string", Description: "认证数据库，默认 admin", Required: false},
		"direct":      {Name: "直连", Type: "boolean", Description: "是否直接连接单个 MongoDB 节点", Required: false},
		"tls":         {Name: "TLS", Type: "boolean", Description: "是否启用 TLS", Required: false},
	}
}

func (c *Connector) New(raw interfaces.ConnectorConfig) (interfaces.Connector, error) {
	var cfg config
	if err := mapstructure.Decode(raw, &cfg); err != nil {
		return nil, fmt.Errorf("decode mongodb config: %w", err)
	}
	if strings.TrimSpace(cfg.Host) == "" || cfg.Port <= 0 || strings.TrimSpace(cfg.Database) == "" {
		return nil, fmt.Errorf("mongodb host, port and database are required")
	}
	if cfg.AuthSource == "" {
		cfg.AuthSource = "admin"
	}
	return &Connector{config: &cfg, enabled: c.enabled}, nil
}

func (c *Connector) Connect(ctx context.Context) error {
	if c.client != nil {
		return nil
	}
	uri := fmt.Sprintf("mongodb://%s:%d", c.config.Host, c.config.Port)
	opts := options.Client().ApplyURI(uri).SetDirect(c.config.Direct)
	if c.config.Username != "" {
		opts.SetAuth(options.Credential{Username: c.config.Username, Password: c.config.Password, AuthSource: c.config.AuthSource})
	}
	if c.config.TLS {
		opts.SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		return fmt.Errorf("connect mongodb: %w", err)
	}
	c.client = client
	if err := c.Ping(ctx); err != nil {
		_ = client.Disconnect(ctx)
		c.client = nil
		return err
	}
	return nil
}

func (c *Connector) Ping(ctx context.Context) error {
	if c.client == nil {
		return fmt.Errorf("connector not connected")
	}
	if err := c.client.Ping(ctx, nil); err != nil {
		return fmt.Errorf("ping mongodb: %w", err)
	}
	return nil
}

func (c *Connector) TestConnection(ctx context.Context) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}
	return c.Ping(ctx)
}

func (c *Connector) Close(ctx context.Context) error {
	if c.client == nil {
		return nil
	}
	err := c.client.Disconnect(ctx)
	c.client = nil
	return err
}

func (c *Connector) GetMetadata(ctx context.Context) (map[string]any, error) {
	if err := c.ensureConnected(ctx); err != nil {
		return nil, err
	}
	var status bson.M
	if err := c.client.Database(c.config.Database).RunCommand(ctx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&status); err != nil {
		return nil, fmt.Errorf("get mongodb build info: %w", err)
	}
	return map[string]any{"database": c.config.Database, "version": status["version"]}, nil
}

func (c *Connector) ListIndexes(ctx context.Context) ([]*interfaces.IndexMeta, error) {
	if err := c.ensureConnected(ctx); err != nil {
		return nil, err
	}
	names, err := c.client.Database(c.config.Database).ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("list mongodb collections: %w", err)
	}
	sort.Strings(names)
	result := make([]*interfaces.IndexMeta, 0, len(names))
	for _, name := range names {
		if strings.HasPrefix(name, "system.") {
			continue
		}
		result = append(result, &interfaces.IndexMeta{Name: name, Description: "MongoDB collection", Properties: map[string]any{}, Mapping: map[string]interfaces.IndexFieldMeta{}, MappingMeta: map[string]any{}})
	}
	return result, nil
}

func (c *Connector) GetIndexMetaByIdentifier(ctx context.Context, name string) (*interfaces.IndexMeta, error) {
	meta := &interfaces.IndexMeta{Name: name, Description: "MongoDB collection"}
	if err := c.GetIndexMeta(ctx, meta); err != nil {
		return nil, err
	}
	return meta, nil
}

func (c *Connector) GetIndexMeta(ctx context.Context, meta *interfaces.IndexMeta) error {
	if err := c.ensureConnected(ctx); err != nil {
		return err
	}
	coll := c.collection(meta.Name)
	count, err := coll.EstimatedDocumentCount(ctx)
	if err != nil {
		return fmt.Errorf("count mongodb collection: %w", err)
	}
	cursor, err := coll.Find(ctx, bson.D{}, options.Find().SetLimit(defaultSampleSize))
	if err != nil {
		return fmt.Errorf("sample mongodb collection: %w", err)
	}
	defer cursor.Close(ctx)
	mapping := map[string]interfaces.IndexFieldMeta{}
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			return err
		}
		collectFields("", doc, mapping)
	}
	if err := cursor.Err(); err != nil {
		return err
	}
	meta.Mapping = mapping
	meta.Properties = map[string]any{"document_count": count, "sample_size": defaultSampleSize}
	meta.MappingMeta = map[string]any{"database": c.config.Database}
	return nil
}

func collectFields(prefix string, doc map[string]any, out map[string]interfaces.IndexFieldMeta) {
	for key, value := range doc {
		name := key
		if prefix != "" {
			name = prefix + "." + key
		}
		typeName := bsonType(value)
		if old, ok := out[name]; ok && old.Type != typeName {
			typeName = "mixed"
		}
		out[name] = interfaces.IndexFieldMeta{Name: name, Type: typeName, Searchable: true, Attributes: map[string]any{"type": typeName}}
		if nested, ok := value.(bson.M); ok {
			collectFields(name, nested, out)
		}
		if nested, ok := value.(map[string]any); ok {
			collectFields(name, nested, out)
		}
	}
}

func bsonType(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case bool:
		return "bool"
	case int, int32:
		return "int"
	case int64:
		return "long"
	case float32, float64:
		return "double"
	case primitive.Decimal128:
		return "decimal"
	case primitive.DateTime, time.Time:
		return "date"
	case primitive.ObjectID:
		return "objectId"
	case primitive.Binary, []byte:
		return "binary"
	case bson.M, map[string]any:
		return "object"
	case bson.A, []any:
		return "array"
	case nil:
		return "null"
	default:
		return "mixed"
	}
}

func (c *Connector) MapType(native string) string {
	switch strings.ToLower(native) {
	case "bool":
		return interfaces.DataType_Boolean
	case "int", "long":
		return interfaces.DataType_Integer
	case "double", "decimal":
		return interfaces.DataType_Decimal
	case "date":
		return interfaces.DataType_Datetime
	case "binary":
		return interfaces.DataType_Binary
	case "object", "array", "mixed":
		return interfaces.DataType_Json
	default:
		return interfaces.DataType_String
	}
}

func (c *Connector) ExecuteQuery(ctx context.Context, name string, _ *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.QueryResult, error) {
	if err := c.ensureConnected(ctx); err != nil {
		return nil, err
	}
	filter := bson.D{}
	if params != nil && params.FilterCondition != nil {
		return nil, fmt.Errorf("mongodb filter_condition is not supported yet")
	}
	find := options.Find()
	if params != nil {
		find.SetSkip(int64(params.Paging.Offset)).SetLimit(int64(params.Paging.Limit))
		if len(params.OutputFields) > 0 {
			projection := bson.D{}
			for _, f := range params.OutputFields {
				projection = append(projection, bson.E{Key: f, Value: 1})
			}
			find.SetProjection(projection)
		}
		if len(params.Sort) > 0 {
			order := bson.D{}
			for _, field := range params.Sort {
				direction := 1
				if strings.EqualFold(field.Direction, "desc") {
					direction = -1
				}
				order = append(order, bson.E{Key: field.Field, Value: direction})
			}
			find.SetSort(order)
		}
	}
	return c.find(ctx, name, filter, find)
}

func (c *Connector) ExecuteQueryWithDsl(ctx context.Context, name, dsl string) (*interfaces.QueryResult, error) {
	var query map[string]any
	if err := json.Unmarshal([]byte(dsl), &query); err != nil {
		return nil, fmt.Errorf("invalid mongodb query JSON: %w", err)
	}
	return c.executeRaw(ctx, name, query)
}

func (c *Connector) ExecuteRawQuery(ctx context.Context, name string, query map[string]any) (*interfaces.RawQueryResponse, error) {
	result, err := c.executeRaw(ctx, name, query)
	if err != nil {
		return nil, err
	}
	columns := []interfaces.ColumnInfo{}
	if len(result.Entries) > 0 {
		names := make([]string, 0, len(result.Entries[0]))
		for name := range result.Entries[0] {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			columns = append(columns, interfaces.ColumnInfo{Name: name, Type: c.MapType(bsonType(result.Entries[0][name]))})
		}
	}
	total := result.Total
	return &interfaces.RawQueryResponse{Columns: columns, Entries: result.Entries, TotalCount: &total}, nil
}

func (c *Connector) executeRaw(ctx context.Context, name string, query map[string]any) (*interfaces.QueryResult, error) {
	filter := any(bson.D{})
	if value, ok := query["filter"]; ok {
		filter = value
	}
	find := options.Find()
	if value, ok := number(query["skip"]); ok {
		find.SetSkip(value)
	}
	if value, ok := number(query["limit"]); ok {
		find.SetLimit(value)
	}
	if value, ok := query["projection"]; ok {
		find.SetProjection(value)
	}
	if value, ok := query["sort"]; ok {
		find.SetSort(value)
	}
	return c.find(ctx, name, filter, find)
}

func (c *Connector) find(ctx context.Context, name string, filter any, opts *options.FindOptions) (*interfaces.QueryResult, error) {
	coll := c.collection(name)
	total, err := coll.CountDocuments(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("count mongodb documents: %w", err)
	}
	cursor, err := coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("query mongodb documents: %w", err)
	}
	defer cursor.Close(ctx)
	entries := []map[string]any{}
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			return nil, err
		}
		entries = append(entries, normalizeDocument(doc))
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	return &interfaces.QueryResult{Entries: entries, Total: total}, nil
}

func normalizeDocument(doc bson.M) map[string]any {
	out := make(map[string]any, len(doc))
	for k, v := range doc {
		out[k] = normalizeValue(v)
	}
	return out
}
func normalizeValue(v any) any {
	switch x := v.(type) {
	case primitive.ObjectID:
		return x.Hex()
	case primitive.DateTime:
		return x.Time()
	case primitive.Decimal128:
		return x.String()
	case primitive.Binary:
		return x.Data
	case bson.M:
		return normalizeDocument(x)
	case map[string]any:
		return normalizeDocument(bson.M(x))
	case bson.A:
		result := make([]any, len(x))
		for i := range x {
			result[i] = normalizeValue(x[i])
		}
		return result
	default:
		return v
	}
}

func number(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), true
	case json.Number:
		value, err := n.Int64()
		return value, err == nil
	default:
		return 0, false
	}
}
func (c *Connector) ensureConnected(ctx context.Context) error {
	if c.client == nil {
		return c.Connect(ctx)
	}
	return nil
}
func (c *Connector) collection(name string) *mongo.Collection {
	return c.client.Database(c.config.Database).Collection(name)
}

func (c *Connector) CreateIndex(ctx context.Context, name string, _ map[string]any, _ map[string]string) error {
	if err := c.ensureConnected(ctx); err != nil {
		return err
	}
	return c.client.Database(c.config.Database).CreateCollection(ctx, name)
}
func (c *Connector) UpdateIndex(context.Context, string, map[string]any) error {
	return fmt.Errorf("mongodb collection schema updates are not supported")
}
func (c *Connector) DeleteIndex(ctx context.Context, name string) error {
	if err := c.ensureConnected(ctx); err != nil {
		return err
	}
	return c.collection(name).Drop(ctx)
}
func (c *Connector) CheckIndexExist(ctx context.Context, name string) (bool, error) {
	if err := c.ensureConnected(ctx); err != nil {
		return false, err
	}
	names, err := c.client.Database(c.config.Database).ListCollectionNames(ctx, bson.D{{Key: "name", Value: name}})
	return len(names) > 0, err
}
func (c *Connector) ValidateAnalyzer(context.Context, string) (bool, error) { return false, nil }

func (c *Connector) CreateDocuments(ctx context.Context, name string, docs []map[string]any) ([]string, error) {
	values := make([]any, len(docs))
	for i := range docs {
		values[i] = docs[i]
	}
	result, err := c.collection(name).InsertMany(ctx, values)
	if err != nil {
		return nil, err
	}
	return stringifyIDs(result.InsertedIDs), nil
}
func (c *Connector) IndexDocuments(ctx context.Context, name string, docs map[string]map[string]any) ([]string, error) {
	ids := make([]string, 0, len(docs))
	for id, doc := range docs {
		filter, err := idFilter(id)
		if err != nil {
			return nil, err
		}
		if _, err := c.collection(name).ReplaceOne(ctx, filter, doc, options.Replace().SetUpsert(true)); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}
func (c *Connector) GetDocument(ctx context.Context, name, id string) (map[string]any, error) {
	filter, err := idFilter(id)
	if err != nil {
		return nil, err
	}
	var doc bson.M
	if err := c.collection(name).FindOne(ctx, filter).Decode(&doc); err != nil {
		return nil, err
	}
	return normalizeDocument(doc), nil
}
func (c *Connector) GetDocuments(ctx context.Context, name string, ids []string) ([]map[string]any, error) {
	values := make([]any, 0, len(ids))
	for _, id := range ids {
		filter, err := idFilter(id)
		if err != nil {
			return nil, err
		}
		values = append(values, filter[0].Value)
	}
	result, err := c.find(ctx, name, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: values}}}}, options.Find())
	if err != nil {
		return nil, err
	}
	return result.Entries, nil
}
func (c *Connector) DeleteDocument(ctx context.Context, name, id string) error {
	filter, err := idFilter(id)
	if err != nil {
		return err
	}
	_, err = c.collection(name).DeleteOne(ctx, filter)
	return err
}
func (c *Connector) UpsertDocuments(ctx context.Context, name string, requests []map[string]any) ([]string, error) {
	ids := []string{}
	for _, request := range requests {
		id, ok := request["_id"].(string)
		if !ok || id == "" {
			return nil, fmt.Errorf("mongodb upsert requires string _id")
		}
		filter, err := idFilter(id)
		if err != nil {
			return nil, err
		}
		delete(request, "_id")
		if _, err := c.collection(name).UpdateOne(ctx, filter, bson.D{{Key: "$set", Value: request}}, options.Update().SetUpsert(true)); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}
func (c *Connector) DeleteDocuments(ctx context.Context, name string, ids []string) error {
	values := make([]any, 0, len(ids))
	for _, id := range ids {
		filter, err := idFilter(id)
		if err != nil {
			return err
		}
		values = append(values, filter[0].Value)
	}
	_, err := c.collection(name).DeleteMany(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: values}}}})
	return err
}
func (c *Connector) DeleteDocumentsByQuery(context.Context, string, *interfaces.ResourceDataQueryParams, []*interfaces.Property) error {
	return fmt.Errorf("mongodb delete by VEGA filter is not supported")
}

func idFilter(id string) (bson.D, error) {
	if objectID, err := primitive.ObjectIDFromHex(id); err == nil {
		return bson.D{{Key: "_id", Value: objectID}}, nil
	}
	return bson.D{{Key: "_id", Value: id}}, nil
}
func stringifyIDs(values []any) []string {
	ids := make([]string, len(values))
	for i, value := range values {
		if id, ok := value.(primitive.ObjectID); ok {
			ids[i] = id.Hex()
		} else {
			ids[i] = fmt.Sprint(value)
		}
	}
	return ids
}
