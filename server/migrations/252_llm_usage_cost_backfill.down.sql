-- No-op: once estimated costs are backfilled, they cannot be distinguished
-- from normal server-estimated writes that happened after this migration.
SELECT 1;
