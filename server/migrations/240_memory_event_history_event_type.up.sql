ALTER TABLE memory_event
    DROP CONSTRAINT IF EXISTS memory_event_event_type_check;

ALTER TABLE memory_event
    ADD CONSTRAINT memory_event_event_type_check
    CHECK (event_type IN ('retain', 'update', 'invalidate', 'delete', 'history'));
