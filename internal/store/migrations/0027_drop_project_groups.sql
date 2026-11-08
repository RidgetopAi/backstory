-- Project groups were removed (decision 8b726bd2). project_groups
-- foreign-keys into projects(key), so a kept table would make a legacy-key
-- sweep or repo-identity merge fail on any store holding a group row.
DROP TABLE IF EXISTS project_groups;
