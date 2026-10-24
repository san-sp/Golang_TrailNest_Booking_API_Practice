CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX activities_title_trgm_idx
    ON activities USING GIN (title gin_trgm_ops);

CREATE INDEX activities_description_trgm_idx
    ON activities USING GIN (description gin_trgm_ops);

CREATE INDEX activities_location_trgm_idx
    ON activities USING GIN (location gin_trgm_ops);
