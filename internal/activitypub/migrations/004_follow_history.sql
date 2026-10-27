ALTER TABLE {{schema}}.outbound_intents ADD COLUMN history jsonb;
ALTER TABLE {{schema}}.outbound_intents ADD CONSTRAINT history_shape CHECK (history IS NULL OR
 (jsonb_typeof(history)='object' AND history ?& ARRAY['server','object_id','payload','interval_ms']
 AND jsonb_typeof(history->'payload')='object' AND history->'payload'->>'id'=activity_id
 AND history->'payload'->'object'->>'id'=history->>'object_id'
 AND (history->>'interval_ms')::integer BETWEEN 1000 AND 60000));
CREATE UNIQUE INDEX history_once_per_server_object ON {{schema}}.outbound_intents
 (publication_id,(history->>'server'),(history->>'object_id')) WHERE history IS NOT NULL;
COMMENT ON COLUMN {{schema}}.outbound_intents.history IS 'Opt-in targeted follow-history metadata. Existing identity/activity IDs and dates, stable retry payload, personal inbox only.';
