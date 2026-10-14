-- IF NOT EXISTS also adopts databases created by the previous startup migrator.
CREATE TABLE IF NOT EXISTS todos (
	id    TEXT PRIMARY KEY,
	title TEXT NOT NULL,
	done  BOOLEAN NOT NULL DEFAULT false
);
