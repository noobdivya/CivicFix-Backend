-- Location of each issue so it can be plotted on the map.
-- Filled in by the "report an issue" feature.
ALTER TABLE issues
    ADD COLUMN address   TEXT NOT NULL DEFAULT '',
    ADD COLUMN latitude  DOUBLE PRECISION,
    ADD COLUMN longitude DOUBLE PRECISION;

CREATE INDEX idx_issues_created_at ON issues(created_at DESC);

-- Messages from public officials shown on the landing page.
-- is_sample marks placeholder text that should be replaced with an
-- approved statement from the real official.
CREATE TABLE official_messages (
    id          SERIAL PRIMARY KEY,
    name        TEXT    NOT NULL,
    designation TEXT    NOT NULL,
    office      TEXT    NOT NULL,
    message     TEXT    NOT NULL,
    is_sample   BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order  INT     NOT NULL DEFAULT 0,
    active      BOOLEAN NOT NULL DEFAULT TRUE
);

INSERT INTO official_messages (name, designation, office, message, sort_order) VALUES
    ('Anita Sharma', 'Deputy Commissioner', 'Municipal Corporation of Delhi (MCD)',
     'A clean, safe and well-maintained city is built together. With CivicFix, every complaint reaches the right department quickly, and every citizen can see exactly how and when it gets resolved. I encourage all residents to report problems in their neighbourhood — your reports help us plan better and act faster.',
     1),
    ('Rakesh Verma', 'Member of Legislative Assembly (MLA)', 'Legislative Assembly Constituency',
     'Good governance begins with listening. CivicFix gives every resident a direct, transparent way to raise civic issues and follow them until they are fixed. Together, let us make our constituency cleaner, safer and more accountable — one report at a time.',
     2);

-- Messages submitted through the landing page contact form.
CREATE TABLE contact_messages (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT        NOT NULL,
    email      TEXT        NOT NULL,
    subject    TEXT        NOT NULL DEFAULT '',
    message    TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
