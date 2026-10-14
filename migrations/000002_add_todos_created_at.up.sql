-- Existing rows receive a timestamp because their actual creation time was
-- never recorded by the original schema.
ALTER TABLE todos ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now();
CREATE INDEX IF NOT EXISTS todos_created_at_id_idx ON todos (created_at, id);
