-- Departments that handle issues. Each category belongs to one department.
CREATE TABLE departments (
    id          SERIAL PRIMARY KEY,
    slug        TEXT NOT NULL UNIQUE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO departments (slug, name, description) VALUES
    ('roads',       'Roads & Infrastructure',          'Potholes, damaged roads and footpaths.'),
    ('electrical',  'Electricity & Street Lighting',   'Power supply faults, wiring and streetlights.'),
    ('water',       'Water Supply & Drainage',         'Drains, waterlogging and pipeline leaks.'),
    ('sanitation',  'Sanitation',                      'Garbage collection and public cleanliness.'),
    ('traffic',     'Traffic Management',              'Traffic signals and road safety equipment.'),
    ('general',     'General Administration',          'Issues that need triage or do not fit elsewhere.');

ALTER TABLE categories ADD COLUMN department_id INT REFERENCES departments(id);

UPDATE categories c SET department_id = d.id
FROM departments d
WHERE (c.slug, d.slug) IN (
    ('pothole', 'roads'), ('road-damage', 'roads'),
    ('electricity', 'electrical'), ('streetlight', 'electrical'),
    ('drainage', 'water'), ('waterlogging', 'water'), ('water-leakage', 'water'),
    ('garbage', 'sanitation'),
    ('traffic-signal', 'traffic'),
    ('other', 'general')
);
UPDATE categories SET department_id = (SELECT id FROM departments WHERE slug = 'general') WHERE department_id IS NULL;

-- Staff accounts. Citizens do not have accounts: they report with their
-- phone number and track with tracking code + phone.
CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    name          TEXT NOT NULL,
    email         TEXT NOT NULL,
    phone         TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('admin', 'department', 'worker')),
    department_id INT REFERENCES departments(id),
    active        BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at TIMESTAMPTZ,
    CHECK (role = 'admin' OR department_id IS NOT NULL)
);
CREATE UNIQUE INDEX users_email_lower ON users (lower(email));
CREATE INDEX idx_users_department_role ON users (department_id, role);

-- Login sessions. Only a SHA-256 hash of the session token is stored.
CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_agent TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_sessions_user ON sessions (user_id);

-- Workflow fields on issues.
ALTER TABLE issues
    ADD COLUMN department_id      INT REFERENCES departments(id),
    ADD COLUMN priority           TEXT NOT NULL DEFAULT 'medium' CHECK (priority IN ('low', 'medium', 'high', 'critical')),
    ADD COLUMN assigned_worker_id BIGINT REFERENCES users(id),
    ADD COLUMN reviewed_at        TIMESTAMPTZ,
    ADD COLUMN assigned_at        TIMESTAMPTZ,
    ADD COLUMN started_at         TIMESTAMPTZ,
    ADD COLUMN due_at             TIMESTAMPTZ,
    ADD COLUMN rejection_reason   TEXT,
    ADD COLUMN updated_at         TIMESTAMPTZ NOT NULL DEFAULT now();

UPDATE issues i SET department_id = c.department_id FROM categories c WHERE c.id = i.category_id;

CREATE INDEX idx_issues_department_status ON issues (department_id, status);
CREATE INDEX idx_issues_worker_status ON issues (assigned_worker_id, status);

-- Timeline of everything that happened to an issue. Public events are
-- shown to the citizen on the tracking page.
CREATE TABLE issue_events (
    id          BIGSERIAL PRIMARY KEY,
    issue_id    BIGINT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    type        TEXT NOT NULL,
    from_status TEXT,
    to_status   TEXT,
    message     TEXT NOT NULL DEFAULT '',
    is_public   BOOLEAN NOT NULL DEFAULT TRUE,
    actor_id    BIGINT REFERENCES users(id) ON DELETE SET NULL,
    actor_name  TEXT NOT NULL DEFAULT '',
    actor_role  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_issue_events_issue ON issue_events (issue_id, created_at);

INSERT INTO issue_events (issue_id, type, to_status, message, actor_role, created_at)
SELECT id, 'reported', 'reported', 'Complaint registered.', 'citizen', created_at FROM issues;

-- In-app notifications for staff.
CREATE TABLE notifications (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    issue_id   BIGINT REFERENCES issues(id) ON DELETE CASCADE,
    title      TEXT NOT NULL,
    body       TEXT NOT NULL DEFAULT '',
    read_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_notifications_user ON notifications (user_id, created_at DESC);
