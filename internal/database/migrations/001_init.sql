-- Issue categories shown on the landing page and used when reporting.
CREATE TABLE categories (
    id          SERIAL PRIMARY KEY,
    slug        TEXT NOT NULL UNIQUE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL,
    icon        TEXT NOT NULL,
    sort_order  INT  NOT NULL DEFAULT 0
);

INSERT INTO categories (slug, name, description, icon, sort_order) VALUES
    ('pothole',        'Potholes',               'Holes and cracks that make roads unsafe for vehicles and pedestrians.', 'pothole',       1),
    ('streetlight',    'Broken Streetlights',    'Dark or flickering streetlights that leave streets unsafe at night.',   'streetlight',   2),
    ('garbage',        'Garbage Accumulation',   'Overflowing bins and uncollected waste piling up in public spaces.',   'garbage',       3),
    ('water-leakage',  'Water Leakage',          'Burst pipes and leaks wasting water and damaging roads.',              'water',         4),
    ('road-damage',    'Damaged Roads',          'Broken pavements, eroded edges and damaged road surfaces.',            'road',          5),
    ('traffic-signal', 'Faulty Traffic Signals', 'Signals that are stuck, dark or out of sync at junctions.',            'traffic',       6);

-- Minimal issue table. Later features (reporting, assignment, tracking)
-- will extend it with location, photos, reporter and assignee columns.
CREATE TABLE issues (
    id          BIGSERIAL PRIMARY KEY,
    category_id INT         NOT NULL REFERENCES categories(id),
    title       TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    status      TEXT        NOT NULL DEFAULT 'reported'
                CHECK (status IN ('reported', 'assigned', 'in_progress', 'resolved', 'rejected')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ
);

CREATE INDEX idx_issues_status ON issues(status);
CREATE INDEX idx_issues_category ON issues(category_id);
