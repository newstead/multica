# UPSYNC-4 (ROL-171): evidence сохранности MultMemory и provider/runtime кастомизаций

Задача: перенести на upstream v0.4.16 integration branch все prod-фичи MultMemory и
provider/runtime кастомизации с evidence и явными заметками о дропнутых фичах.

- База сравнения (production): `origin/production` = `b92b3be5` (v0.4.14-ns.11).
- Целевая ветка: `origin/upstream-sync/v0.4.16-production` = `9d28735c` (post-merge PR #47 и #48).
- Upstream: `v0.4.16` = `3cab03cc`.
- Метод: `git diff origin/production origin/upstream-sync/v0.4.16-production` по файлам фич;
  сверка с upstream для анализа «заменено upstream».

## 1. MultMemory gateway foundation и RFC-driven behavior — сохранено (файлы идентичны)

- `docs/multmemory-memory-gateway-rfc.md` — без изменений.
- `server/internal/service/memory.go` (+`memory_test.go`) — без изменений.
- `server/internal/handler/memory.go`, `memory_capture.go`, `memory_delivery_worker.go` (+`memory_test.go`) — без изменений.
- `server/internal/metrics/multmemory.go` (+`multmemory_test.go`) — без изменений.
- `server/pkg/db/queries/memory.sql`, `server/pkg/db/generated/memory.sql.go`, `server/pkg/protocol/memory.go` — без изменений.
- `server/internal/daemon/execenv/codex_memory.go` (+test) — без изменений.
- `packages/core/memory/*`, `packages/core/types/memory.ts` — без изменений.
- Миграции: `253_memory_gateway`, `254_memory_event_workspace_created_index` — контент идентичен prod (`235`/`236`), `CREATE TABLE IF NOT EXISTS` (идемпотентно).

## 2. Hindsight adapter и board — сохранено (идентично)

- `server/internal/service/memory_hindsight.go` (+`memory_hindsight_test.go`) — без изменений.
- `packages/views/dashboard/components/hindsight-memory-page.tsx` (+test), `apps/web/app/[workspaceSlug]/(dashboard)/usage/hindsight/page.tsx` — без изменений.

## 3. mem0 OSS adapter и board — сохранено (идентично)

- `server/internal/service/memory_mem0.go` (+`memory_mem0_contract_test.go`) — без изменений.
- `packages/views/dashboard/components/mem0-page.tsx`, `packages/views/dashboard/memory/mem0-board-data.ts` (+test), `apps/web/app/[workspaceSlug]/(dashboard)/usage/mem0/page.tsx`, `e2e/mem0-board-screenshots.spec.ts` — без изменений.

## 4. Dual-head delivery/comparison/telemetry — сохранено (идентично)

- `server/internal/handler/memory_delivery_worker.go`, `server/internal/service/memory.go` (primary/shadow, `read_mode` dual) — без изменений.
- `server/internal/metrics/multmemory.go` — без изменений.
- Миграции `255..257` (delivery due index, recall sample, delivery lag pair), `259_memory_dual_write_telemetry` — контент идентичен prod (`237..239`, `241`).

## 5. Workspace memory route/auth behavior — сохранено

- `server/cmd/server/router.go`: memory-блок без изменений — GET `/memory/config`, `/memory/mem0-board`, `/memory/recall-samples`, `/memory/audit`, `/memory/audit/export` и POST `/memory/recall` (member-visible), PUT `/memory/config`, POST `/memory/events/retain`, POST `/memory/audit/{eventId}/correct|invalidate`, DELETE `/memory/audit/{eventId}`, POST `/memory/erase` (admin-gated).
- Auth-логика в `server/internal/handler/memory.go` — без изменений.
- Дополнение (не регрессия): `DeleteWorkspaceMemory` в `server/internal/handler/workspace.go` + `workspace_delete.sql` sweep 5 memory-таблиц — из PR #48.

## 6. DeepSeek runtime provider/default model — сохранено

- `server/pkg/agent/agent.go`: `"deepseek"` в `SupportedTypes` и `New()`.
- `server/pkg/agent/version.go`: `MinVersions["deepseek"] = "0.145.0"` — идентично prod.
- `server/pkg/agent/models.go`: `deepseekStaticModels()` → `deepseek-v4-flash` с `Default: true` — идентично prod.
- `server/internal/daemon/agents_probe.go`: DeepSeek роутится через codex binary, `e.Model = "deepseek-v4-flash"` — сохранено.
- `server/internal/daemon/config.go`: deepseek в карте провайдеров и сообщении об установке CLI.
- `server/internal/daemon/execenv/codex_home.go` (+`deepseek_codex_home_test.go`): `ensureDeepSeekCodexProviderConfig` (codex `config.toml` model_provider deepseek) — сохранено.
- `server/internal/daemon/execenv/runtime_config.go`: `deepseek` в списке config kinds; `local_skills.go`, `runtime_mcp.go`: `case "codex", "deepseek"`.
- `server/internal/daemon/daemon.go`: `"deepseek": "DeepSeek"` display label.
- Frontend: `packages/core/runtimes/display.ts` `deepseek: "DeepSeek"` override; `packages/core/types/agent.ts` — без изменений по deepseek.
- Миграция `258_runtime_profile_add_deepseek`: union-constraint `deepseek` + `qoderclicn` (семантический merge с upstream 242) — единственное осознанное изменение контента миграции.

## 7. Kimi cost/usage behavior — сохранено (cost pricing в prod отсутствует)

- `server/pkg/agent/kimi.go`: сохранён prod-код usage merge (`mergeACPPromptUsage`, учёт `ReasoningTokens`/cache); добавлены только upstream-изменения (drain grace, activity-канал).
- `server/internal/metrics/pricing.go`: идентичен prod; kimi-cost записей нет ни в prod, ни в integration (есть только `deepseek:v4-pro`/`v4-flash`) — дропа нет, поведение не менялось.
- Kimi provider и usage telemetry (tokens) — upstream-фича, присутствует на обеих ветках.

## 8. Russian language policy backend/runtime hooks — сохранено

- `server/internal/handler/language_policy.go` (+test) — без изменений.
- `server/internal/handler/agent.go`: `LanguageCodes`/`LanguagePolicy` поля сохранены (изменения только gofmt-выравнивание).
- `server/internal/daemon/types.go`: `Task.LanguagePolicy` сохранён (изменения — выравнивание + upstream-поля).
- `server/internal/daemon/execenv/execenv.go` (+`language_policy_test.go`), `runtime_config_sections.go` (Language Policy в brief sections) — сохранено.
- `server/pkg/db/queries/agent.sql`: `ListAllAgentsAnyKind` включает `role_code`, `language_codes`, `language_policy` (восстановлено в PR #48); update/NULL-clear запросы на месте.
- Миграции `261..264` (workspace/project/agent language policy, supported codes) — контент идентичен prod (`243..246`).

## Миграции: реконсиляция 233..246 → 251..264

| Prod (база) | Integration | Контент |
|---|---|---|
| 233 `task_usage_reasoning_tokens` | 251 | идентичен |
| 234 `llm_usage_cost_backfill` | 252 | идентичен |
| 235 `memory_gateway` | 253 | идентичен |
| 236 `memory_event_workspace_created_index` | 254 | идентичен |
| 237 `memory_provider_delivery_due_index` | 255 | идентичен |
| 238 `memory_recall_sample_workspace_sampled_index` | 256 | идентичен |
| 239 `memory_delivery_lag_recall_pair` | 257 | идентичен |
| 240 `runtime_profile_add_deepseek` | 258 | **изменён**: union `deepseek`+`qoderclicn` (семантический merge) |
| 241 `memory_dual_write_telemetry` | 259 | идентичен |
| 242 `agent_identity_metadata` | 260 | идентичен |
| 243 `workspace_language_policy` | 261 | идентичен |
| 244 `project_language_policy` | 262 | идентичен |
| 245 `agent_language_policy` | 263 | идентичен |
| 246 `language_policy_supported_codes` | 264 | идентичен |

Upstream v0.4.16 занимает 001..250; коллизий нет (проверено lint-тестом `TestMigrationNumericPrefixesStayUniqueAfterLegacySet`).

## Дропнутые / заменённые upstream фичи

- В scope нет фич, заменённых upstream: upstream v0.4.16 не содержит memory-стека (MultMemory/mem0/Hindsight — наши-only файлы), DeepSeek runtime и language policy у upstream отсутствуют — заменять нечего.
- `apps/docs` (zh-файлы) — часть upstream docs-рефакторинга, вне scope данной задачи; отмечено в ROL-169/QA как принятое расхождение.

## Верификация (ветка `agent/upsync-dev/rol-171-multmemory-provider-carry`, база `9d28735c`)

- `go build ./...` — ok.
- `go test ./internal/migrations/...` — ok.
- `go test ./internal/metrics/...` — ok.
- `go test ./pkg/agent/...` — ok.
- `go test ./internal/daemon/execenv/ -run 'LanguagePolicy|DeepSeek|Kimi'` — ok.
- Ограничения окружения (не регрессии): handler/service memory-тесты требуют Postgres (pre-existing, воспроизводятся на prod-базе); `pnpm typecheck` — node_modules не установлены. Live-прогон миграций 251..264 на схеме v0.4.14-ns.11 — на release/QA gate.
