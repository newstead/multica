# Agent Identity Metadata

Agent identity badges use explicit, project-independent metadata on agent rows:

- `role_code TEXT NULL`
- `language_codes TEXT[] NULL`

This normalized schema was chosen over a versioned structured avatar payload because it is smaller, validates naturally at the API and database boundary, works with sqlc/generated clients, and can evolve through ordinary migrations when the taxonomy changes. Existing `avatar_url` values remain backward compatible; member avatars are unchanged.

## Taxonomy

Role codes: `TL`, `BE`, `FE`, `FS`, `QA`, `OPS`, `ML`, `DA`, `SRE`, `SEC`.

Language codes: `GO`, `PY`, `TS`, `JS`, `RS`, `SH`, `RB`, `JV`, `KT`, `SW`, `CS`, `CP`, `SC`, `EL`.

The API normalizes writes by trimming whitespace, uppercasing codes, dropping empty language tokens, deduping languages, and sorting languages alphabetically. Unknown values are rejected on create/update with a `400` validation error. Reads are passive: clients should treat unknown future codes as display data and render the raw code rather than inferring identity from names, project names, models, or instructions.

## API and CLI

Agent create/update JSON accepts:

```json
{
  "role_code": "BE",
  "language_codes": ["GO", "PY"]
}
```

Update is tri-state:

- omitted: preserve current value
- `role_code: null` or `role_code: ""`: clear the role
- `language_codes: null` or `language_codes: []`: clear languages
- non-empty values: normalize, validate, and replace

The CLI mirrors these fields:

```bash
multica agent create --name aq-go --runtime-id <id> --role-code BE --language-code GO
multica agent update <id> --role-code QA --language-code GO --language-code TS
multica agent update <id> --role-code '' --language-code ''
```

## Migration and Fallback

Migration `242_agent_identity_metadata` adds nullable columns and database checks for the approved taxonomy. The down migration drops the checks and columns.

Legacy agents keep `NULL` identity values and continue using their existing emoji/image `avatar_url`. Badge renderers should suppress the identity badge when both fields are unset. If a future backend returns a code unknown to an older client, the client should render the raw code with an unknown/raw tooltip fallback rather than guessing from other fields.

Identity writes record an `agent_identity_updated` activity row containing only agent id/name and before/after codes.
