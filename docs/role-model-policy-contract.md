# ROLEPOL-0: архитектурный контракт workspace role → AI execution config

Версия контракта: 1.0 (lead, role-policy-lead). Статус: передан на реализацию (dev), затем QA.
Файл зафиксирован в интеграционной ветке `workspace-role-model-policy`; идентичен тексту контракта
в описании задачи ROL-214 (и родительской ROL-207).

## 1. Цель

Workspace-level feature: владелец/admin включает per-workspace матрицу «роль агента → AI execution config»
(agent / runtime / model / thinking_level / service_tier, optional fallback/disabled) и workspace routing
применяет её для новых задач. При выключенной фиче или отсутствии правила для роли — старое
agent-centric поведение сохраняется без изменений.

## 2. Текущее состояние кода (факты)

- `agent.role_code` — метаданные агента, canonical значения: `TL, BE, FE, FS, QA, OPS, ML, DA, SRE, SEC`
  (`server/internal/handler/agent.go`: `validAgentRoleCodes`, `orderedAgentRoleCodes`; frontend-список в
  `packages/views/common/actor-avatar.tsx` `ROLE_CODES`).
- Task enqueue штампует `agent_task_queue.runtime_id = agent.runtime_id`:
  `server/internal/service/task.go` — `enqueueIssueTaskWithCommentPlan`, `enqueueMentionTaskWithCommentPlan`,
  `EnqueueDeferredAssigneeFallback`; autopilot-путь — `server/internal/service/autopilot.go` (~`CreateAutopilotTask`).
- Daemon claim отдаёт agent-scoped execution config: `server/internal/handler/daemon.go`
  `buildClaimedTaskResponse` → `TaskAgentData{Model, ThinkingLevel, ServiceTier}` из строки агента.
  Daemon применяет их в `server/internal/daemon/daemon.go` (~5309) с валидацией против runtime-каталога
  (`agent.ValidateServiceTier`, `agent.ValidateThinkingLevel`, `server/pkg/agent`).
- Workspace-level настройки: `workspace.settings` JSONB (пример: `comment_routing.escalation_seconds` в
  `server/internal/handler/comment.go`). Булевы workspace-политики добавляются колонкой (пример:
  `workspace.attribution_fail_closed`). Обновление workspace: `PUT/PATCH /api/workspaces/{id}` под
  middleware `RequireWorkspaceRoleFromURL("owner","admin")`.
- Web UI настроек workspace: `packages/views/settings/components/workspace-tab.tsx` (прецедент поля:
  `language-policy-field.tsx`).

## 3. Каноническое пространство ролей

Ключ политики — существующий `role_code` (нормализуется в uppercase, без пробелов). Новый параллельный
пространства имён не вводим. UI-метки для человекочитаемых ролей из описания задачи:

| plain role (UI label) | role_code (canonical key) |
|---|---|
| lead | `TL` |
| dev (generic backend) | `BE` |
| frontend | `FE` |
| fullstack | `FS` |
| qa | `QA` |
| ops | `OPS` |
| ml | `ML` |
| data | `DA` |
| sre | `SRE` |
| security | `SEC` |

Матрица в UI показывает все 10 строк; отсутствие правила = `agent_default`.

## 4. Модель данных (миграция)

Решение: first-class таблица `workspace_role_policy`, а не JSON в `workspace.settings`, потому что
нужны FK-валидация (`workspace_id`, `agent_id`, `runtime_id`, `updated_by`), CHECK-ограничения и
аудит (`created_at`/`updated_at`/`updated_by`), которых JSONB-подход не даёт.

Новая миграция `265_workspace_role_policy.up.sql` / `.down.sql` в `server/migrations/` (номер 265
свободен: последняя миграция на production — `264_language_policy_supported_codes`):

```sql
-- up
ALTER TABLE workspace
  ADD COLUMN role_policy_enabled boolean NOT NULL DEFAULT false;

CREATE TABLE workspace_role_policy (
  workspace_id  uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  role_code     text NOT NULL,
  -- Вариант A: жёсткая привязка к конкретному AI-агенту (XOR с exec-конфигом).
  agent_id      uuid NULL REFERENCES agent(id) ON DELETE SET NULL,
  -- Вариант B: exec-конфиг поверх назначенного агента (все поля опциональны).
  runtime_id    uuid NULL REFERENCES agent_runtime(id) ON DELETE SET NULL,
  model         text NULL,
  thinking_level text NULL,
  service_tier  text NULL,
  fallback      text NOT NULL DEFAULT 'agent_default', -- 'agent_default' | 'disabled'
  updated_by    uuid NULL REFERENCES app_user(id) ON DELETE SET NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, role_code),
  CONSTRAINT workspace_role_policy_agent_xor_exec CHECK (
    agent_id IS NULL OR (runtime_id IS NULL AND model IS NULL AND thinking_level IS NULL AND service_tier IS NULL)
  ),
  CONSTRAINT workspace_role_policy_fallback_check CHECK (fallback IN ('agent_default','disabled')),
  CONSTRAINT workspace_role_policy_role_code_check CHECK (
    role_code IN ('TL','BE','FE','FS','QA','OPS','ML','DA','SRE','SEC')
  )
);
CREATE INDEX workspace_role_policy_workspace_idx ON workspace_role_policy(workspace_id);
```

`agent_task_queue` получает 3 nullable колонки (exec-overrides; agent-binding и runtime-binding
переиспользуют существующие `agent_id` / `runtime_id` задачи):

```sql
ALTER TABLE agent_task_queue
  ADD COLUMN policy_model text NULL,
  ADD COLUMN policy_thinking_level text NULL,
  ADD COLUMN policy_service_tier text NULL;
```

После миграции — регенерация sqlc (`server/pkg/db/generated/*`) и обновление моделей.

## 5. Семантика разрешения (resolver)

Единая функция в `TaskService` (например `ResolveRolePolicy(ctx, workspaceID, agent) -> RolePolicyResolution`).
Вход: workspace + целевой агент задачи (по его `role_code`). Выход: режим + переопределения.

Порядок:

1. `workspace.role_policy_enabled = false` ИЛИ у агента нет `role_code` ИЛИ нет строки для этого
   role_code → `agent_default` (текущее поведение, ничего не меняем).
2. Строка с `fallback = 'disabled'` → enqueue отказывается (fail-closed, как attribution fail-closed):
   `CreateAgentTask` не создаётся, вызывающему возвращается понятная ошибка (в комментарий на issue
   — на усмотрение dev, по аналогии с существующими refused-кейсами).
3. Строка с `agent_id` → `bind_agent`: `task.agent_id = policy agent`, `task.runtime_id = policy agent.runtime_id`.
   Если policy agent архивирован или без runtime → warn + деградация в `agent_default` (не fail).
4. Строка без `agent_id` → `exec_override`: `task.agent_id = назначенный агент` (без изменений),
   `task.runtime_id = rule.runtime_id ?? agent.runtime_id`; `task.policy_model / policy_thinking_level /
   policy_service_tier = rule.*` (nullable).

Правила:

- Резолвится ТОЛЬКО при enqueue (новые задачи). Уже enqueued/running задачи не переписываются.
- `agent_id` binding побеждает exec-поля (CHECK не даёт их смешивать в БД; API валидирует то же самое).
- Режим `bind_agent` и `exec_override` в лог enqueue: `role_code`, matched rule, resolved runtime/model.
- Chat/direct-message задачи (путь `CreateChatTask`) в v1 НЕ покрываются политикой (agent-centric),
  это явно задокументировать в коде и UI.

## 6. Точки внедрения (backend)

1. **Enqueue** — применить resolver перед `CreateAgentTask`:
   - `server/internal/service/task.go`: `enqueueIssueTaskWithCommentPlan`, `enqueueMentionTaskWithCommentPlan`
     (покрывает mention, thread-parent, squad-leader через `EnqueueTaskForSquadLeader*`), `EnqueueDeferredAssigneeFallback`.
   - `server/internal/service/autopilot.go`: путь `CreateAutopilotTask`.
   - Внимание: dedup/coalescing pending-task по (issue, executor) — использовать резолвнутого executor'а
     (`policy agent`), чтобы два rule-агента, резолвящиеся в одного executor'а, coalescились корректно.
2. **Claim** — `server/internal/handler/daemon.go` `buildClaimedTaskResponse`: при непустых
   `task.policy_model/policy_thinking_level/policy_service_tier` отдавать их в `TaskAgentData`
   (приоритет над полями агента). Daemon-side валидация (ValidateServiceTier/ValidateThinkingLevel)
   остаётся как safety net и работает без изменений.
3. Существующая двухуровневая model-резолюция в daemon (`agent.model` → env) сохраняется: policy_model
   встаёт на место agent.model.

## 7. API

- `GET  /api/workspaces/{workspaceId}/role-policy` → `{ enabled: bool, rules: { "<ROLE_CODE>": { agent_id, runtime_id, model, thinking_level, service_tier, fallback } } }`.
  Доступ: member.
- `PUT  /api/workspaces/{workspaceId}/role-policy` — полная замена матрицы + `enabled`. Доступ:
  owner/admin (middleware `RequireWorkspaceRoleFromURL(...,"owner","admin")`, как у `UpdateWorkspace`).
- Валидация на сервере: role_code из canonical set; agent_id ∈ workspace, не archived, имеет runtime;
  runtime_id ∈ workspace; model/thinking_level/service_tier валидны для (provider(runtime), model) через
  существующие `agent.ValidateServiceTier` / `agent.ValidateThinkingLevel`; XOR agent_id vs exec-поля.
  Ошибки возвращать 400 с конкретным указанием поля/роли.

## 8. Web UI

- Секция «Role policy» в `packages/views/settings/components/workspace-tab.tsx` (или отдельный
  компонент `role-policy-editor.tsx` в `packages/views/settings/components/`).
- Содержимое: toggle `enabled`; таблица 10 ролей (label + role_code); на строку — выбор режима
  (agent | exec config | no rule), agent-picker (существующие UI-компоненты выбора агента), для exec —
  runtime picker + model/thinking_level/service_tier (select'ы на основе runtime model catalog endpoint),
  fallback select (`agent_default`/`disabled`); кнопка Save → PUT.
- Секция видна всем member'ам, редактирование только owner/admin (скрывать/дизейблить для остальных).
- При `enabled=true` и пустой матрице — предупреждение. Тесты компонентов по образцу
  `language-policy-field.test.tsx`.

## 9. Backward compatibility и безопасность

- По умолчанию `role_policy_enabled = false` → поведение не меняется ни на одном пути.
- Миграция аддитивная (новые колонки/таблица), down-миграция полная.
- Никаких изменений credentials, деплоя, рестарта, merge в production/main/multmemory — только PR в
  `workspace-role-model-policy` (feature branch от `origin/workspace-role-model-policy` после её создания
  от `origin/production`).
- Политика не должна давать агенту больше прав, чем у назначенного: resolver не меняет attribution/
  authorization (originator/accountable остаются как сейчас).

## 10. Тесты (definition of done для dev)

- Resolver unit-тесты: feature off / нет role_code / нет правила / `bind_agent` / `exec_override` /
  `disabled` / архивированный policy agent (деградация).
- Enqueue integration-тесты: issue assignment, mention, squad-leader, deferred fallback, autopilot —
  проверка заштампованных `agent_id`/`runtime_id`/policy-колонок.
- Handler-тесты: auth (member vs admin), валидация (плохой role_code, чужой/архивированный agent,
  невалидный model/thinking/service_tier), roundtrip GET/PUT.
- Claim-тест: task с policy-колонками → `TaskAgentData` несёт overrides; без колонок → значения агента.
- Миграция up/down (есть паттерн migration-тестов в `server/internal/migrations/`).
- Frontend: компонентный тест toggle/матрицы/сохранения/ошибок.
- Прогон: `go test ./server/internal/... ./server/pkg/...` (или целевые пакеты) + frontend-тесты
  затронутых пакетов; приложить вывод в PR.

## 11. Что вне скоупа v1

- Chat/direct-message задачи, live-перезапись уже enqueued задач, кэш с инвалидацией (можно простой
  TTL-кэш policy на 30–60s, без жёсткой инвалидации), деплой/релиз, метрики в observability-дашборды.

## Приложение A. Branch evidence (lead, 2026-08-03)

- Интеграционная ветка `workspace-role-model-policy` существует на origin.
- `origin/workspace-role-model-policy` = `dda5fe6c8b233e79a1c9161700f0d39b8a8e4935` =
  `origin/production` (тот же commit: `dda5fe6c8 feat(ops): add workspace metrics endpoint (#56) (#58)`).
  База корректна: ветка создана ровно от текущего `origin/production`.

## Приложение B. Проверка точек внедрения на origin/production @ dda5fe6c8 (lead)

- `server/internal/handler/agent.go:41,54` — `validAgentRoleCodes` / `orderedAgentRoleCodes` ✓
- `server/internal/service/task.go:1037` — `enqueueIssueTaskWithCommentPlan`; `:1160` —
  `enqueueMentionTaskWithCommentPlan`; `:1239` — `EnqueueDeferredAssigneeFallback` ✓
- Autopilot: отдельной функции `CreateAutopilotTask` нет (в контракте была ссылка «~»). Реальные пути:
  `server/internal/service/autopilot.go` вызывает `TaskSvc.EnqueueTaskForIssue(WithHandoff)` /
  `TaskSvc.EnqueueTaskForSquadLeader(WithHandoff)`; squad-leader-маршрутизация также через
  `enqueueSquadLeaderTask` (`server/internal/service/issue.go:549`). Resolver-хук естественно ложится
  в эти TaskService-методы — пункт 6.1 остаётся в силе.
- `server/internal/handler/daemon.go:1590` — `buildClaimedTaskResponse`; `TaskAgentData` —
  `server/internal/handler/agent.go:624` ✓
- Прецеденты: `workspace.attribution_fail_closed` (bool-колонка), `comment_routing.escalation_seconds`
  (JSONB `workspace.settings`) ✓
- UI: `packages/views/settings/components/workspace-tab.tsx`, `language-policy-field.tsx` (+ `.test.tsx`) ✓
- Миграции: последняя на production — `264_language_policy_supported_codes`, номер `265` свободен ✓
