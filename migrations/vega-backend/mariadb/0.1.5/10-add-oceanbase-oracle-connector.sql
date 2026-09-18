-- Copyright openbkn.ai
-- Copyright The kweaver.ai Authors.
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

-- Register the built-in OceanBase connector for Oracle-compatible tenants.
INSERT INTO t_connector_type (f_type, f_name, f_description, f_mode, f_category, f_enabled)
SELECT 'oceanbase_oracle', 'oceanbase_oracle', 'OceanBase Oracle 兼容模式关系型数据库连接器', 'local', 'table', TRUE
FROM DUAL
WHERE NOT EXISTS (
    SELECT f_type FROM t_connector_type WHERE f_type = 'oceanbase_oracle'
);
