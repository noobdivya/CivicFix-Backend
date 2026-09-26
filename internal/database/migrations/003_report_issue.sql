-- Categories requested for issue reporting.
UPDATE categories
SET name = 'Road Problems',
    description = 'Broken pavements, cracks, eroded edges and other damaged road surfaces.'
WHERE slug = 'road-damage';

INSERT INTO categories (slug, name, description, icon, sort_order) VALUES
    ('electricity',  'Electricity',  'Power cuts, hanging or exposed wires, sparking poles and faulty transformers.', 'electricity',  0),
    ('drainage',     'Drainage',     'Blocked or overflowing drains, open manholes and sewage problems.',            'drainage',     0),
    ('waterlogging', 'Waterlogging', 'Water collected on roads, underpasses or residential lanes.',                  'waterlogging', 0),
    ('other',        'Other',        'Any other civic problem that does not fit the categories above.',             'other',        0);

UPDATE categories SET sort_order = CASE slug
    WHEN 'pothole'        THEN 1
    WHEN 'road-damage'    THEN 2
    WHEN 'electricity'    THEN 3
    WHEN 'streetlight'    THEN 4
    WHEN 'drainage'       THEN 5
    WHEN 'waterlogging'   THEN 6
    WHEN 'water-leakage'  THEN 7
    WHEN 'garbage'        THEN 8
    WHEN 'traffic-signal' THEN 9
    WHEN 'other'          THEN 10
    ELSE sort_order END;

-- Reporter details and a public tracking code for each issue.
-- The Aadhaar number itself is never stored: only its last 4 digits (for
-- display) and a keyed HMAC-SHA256 hash (to recognise repeat reporters).
ALTER TABLE issues
    ADD COLUMN tracking_code          TEXT UNIQUE,
    ADD COLUMN area                   TEXT NOT NULL DEFAULT '',
    ADD COLUMN reporter_name          TEXT,
    ADD COLUMN reporter_phone         TEXT,
    ADD COLUMN reporter_aadhaar_last4 TEXT,
    ADD COLUMN reporter_aadhaar_hash  TEXT;

CREATE INDEX idx_issues_area ON issues(area);

-- Photos attached to an issue. kind = 'report' (from the citizen) or
-- 'completion' (proof uploaded by a field worker, used by a later feature).
CREATE TABLE issue_photos (
    id           BIGSERIAL PRIMARY KEY,
    issue_id     BIGINT      NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    kind         TEXT        NOT NULL DEFAULT 'report' CHECK (kind IN ('report', 'completion')),
    file_path    TEXT        NOT NULL, -- relative to the upload directory
    content_type TEXT        NOT NULL,
    size_bytes   INT         NOT NULL,
    width        INT         NOT NULL,
    height       INT         NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_issue_photos_issue ON issue_photos(issue_id);
