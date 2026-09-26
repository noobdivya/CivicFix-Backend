-- Aadhaar is no longer collected. Delete what was stored for earlier reports.
ALTER TABLE issues
    DROP COLUMN IF EXISTS reporter_aadhaar_last4,
    DROP COLUMN IF EXISTS reporter_aadhaar_hash;
