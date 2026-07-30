DELETE FROM memory_provider_delivery
WHERE memory_event_id IN (
    SELECT id FROM memory_event WHERE event_type = 'history'
);

DELETE FROM memory_event
WHERE event_type = 'history';

ALTER TABLE memory_event
    DROP CONSTRAINT IF EXISTS memory_event_event_type_check;

ALTER TABLE memory_event
    ADD CONSTRAINT memory_event_event_type_check
    CHECK (event_type IN ('retain', 'update', 'invalidate', 'delete'));
