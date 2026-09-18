-- Register the built-in MongoDB connector.
INSERT INTO t_connector_type (f_type, f_name, f_description, f_mode, f_category, f_enabled)
SELECT 'mongodb', 'mongodb', 'MongoDB 文档数据库连接器', 'local', 'index', TRUE
FROM DUAL WHERE NOT EXISTS (
    SELECT f_type FROM t_connector_type WHERE f_type = 'mongodb'
);
