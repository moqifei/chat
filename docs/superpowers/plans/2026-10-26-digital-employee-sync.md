# Digital Employee Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add QA/local-gated daily digital employee snapshot synchronization to Chat, reuse the AD organization tables, control the existing digital twin per target user, and prevent digital employees from appearing in the client agent views.

**Architecture:** The Chat RPC owns the scheduled and manual orchestration. It calls the configured external employee API, persists accounts through the existing register/account/attribute/credential models, preserves a fixed virtual department across AD cleanup, and replaces only that department's active membership. Digital twin enable/disable is performed in-process by merging only the existing `openim_digital_twin.enabled` field into the target OpenIM user's `Ex`.

**Tech Stack:** Go 1.22+, MongoDB repository interfaces, Viper configuration, existing Chat RPC/REST bridge, OpenIM IM caller, React/TypeScript Electron client.

## Global Constraints

- Digital employee synchronization is disabled by default and enabled only by local/QA deployment configuration.
- External paths are `/digital-employees/employeelist` and `/digital-employees/external-bind` appended to one configured `baseURL`.
- `employeeCode` is permanently unique; credential key is `de:<employeeCode>`; generated IDs are `de_` plus random digits.
- Reuse `register`, `account`, `attribute`, `credential`, `ad_departments`, and `ad_department_members`; do not add a digital employee master table or generic source column.
- Default email is `12345678901@aiehr.com`, default phone is `12345678901`, and department ID is `OU=数字员工,OU=ZXBXUsers,DC=qa,DC=bx`.
- The external list is a complete snapshot. Only `status=1` is an active department member; `status=2` and snapshot absence remove membership and disable the twin without deleting account data.
- Existing AD synchronization must preserve the fixed digital employee department only when digital employee sync is enabled.
- Client agent filtering must accept only `register_type=2`; organization synchronization continues to call the single `/ad/sync` endpoint.

---

### Task 1: Add source constants, configuration, and protected department storage APIs

**Files:**
- Modify: `pkg/common/constant/constant.go`
- Modify: `pkg/common/config/config.go`
- Modify: `config/chat-rpc-chat.yml`
- Modify: `pkg/common/db/table/chat/department.go`
- Modify: `pkg/common/db/table/chat/department_member.go`
- Modify: `pkg/common/db/model/chat/department.go`
- Modify: `pkg/common/db/model/chat/department_member.go`
- Modify: `internal/rpc/chat/sync_ad.go`
- Test: `pkg/common/constant/constant_test.go`

**Interfaces:**
- Produces `DigitalEmployeeRegister`, `CredentialDigitalEmployee`, and `config.DigitalEmployee`.
- Produces `DeleteAllExceptDepartmentID(ctx, departmentID)` on both department repository interfaces.

- [ ] **Step 1: Write failing tests** for the new register/credential values and protected deletion filter behavior at the repository query boundary.
- [ ] **Step 2: Run the focused Go tests and verify they fail because the constants and methods do not exist.**
- [ ] **Step 3: Add the constants, config fields, YAML defaults, repository interface methods, and Mongo `DeleteMany` filters using `department_id: {$ne: protectedID}`.**
- [ ] **Step 4: Change AD synchronization to call the protected deletion methods only when `DigitalEmployee.Enable` is true, otherwise retain `DeleteAll`.**
- [ ] **Step 5: Run `go test ./pkg/common/... ./internal/rpc/chat -run 'DigitalEmployee|Department' -count=1` and verify the focused tests pass.**

### Task 2: Implement snapshot validation, account persistence, and external API client

**Files:**
- Create: `internal/rpc/chat/sync_digital_employee.go`
- Test: `internal/rpc/chat/sync_digital_employee_test.go`
- Modify: `internal/rpc/chat/utils.go`
- Modify: `pkg/common/db/database/chat.go`

**Interfaces:**
- `digitalEmployee` and `digitalEmployeeSnapshotPlan` model the external response.
- `planDigitalEmployeeSnapshot([]digitalEmployee) (digitalEmployeeSnapshotPlan, error)` validates required fields, duplicate codes, and statuses.
- `BuildCredentialDigitalEmployee(employeeCode string) string` returns `de:` plus the employee code.

- [ ] **Step 1: Write failing tests** for active/inactive partitioning, duplicate rejection, invalid status rejection, credential formatting, and JSON endpoint response handling.
- [ ] **Step 2: Run the focused tests and verify failure is caused by missing implementation.**
- [ ] **Step 3: Implement the pure plan and HTTP client with context timeout, response code validation, complete snapshot decoding, and fixed endpoint suffixes.**
- [ ] **Step 4: Implement new-user creation using `RegisterUser` with default fields, `register_type=4`, one `de:` credential, and collision-checked `de_` user IDs.**
- [ ] **Step 5: Add an attribute update helper through the existing database interface so rehires/name changes update local profile fields without adding a new table.**
- [ ] **Step 6: Run focused tests and `go vet ./internal/rpc/chat ./pkg/common/db/...`.**

### Task 3: Integrate full snapshot lifecycle, OpenIM registration, twin state, and callbacks

**Files:**
- Modify: `internal/rpc/chat/sync_digital_employee.go`
- Modify: `internal/rpc/chat/sync_ad.go`
- Modify: `internal/rpc/chat/sync_ad_api.go`
- Modify: `internal/rpc/chat/start.go`
- Test: `internal/rpc/chat/sync_digital_employee_test.go`

**Interfaces:**
- `runDigitalEmployeeSync(ctx)` performs one validated full snapshot synchronization.
- `setDigitalTwinEnabled(ctx, userID, enabled)` updates only `openim_digital_twin.enabled` through existing `digitaltwin` helpers and `IMCaller.UpdateUserEx`.
- `SyncADOrganization` runs AD first and runs digital employees only when configured enabled.

- [ ] **Step 1: Add failing lifecycle tests** for new employee creation, rehire userID reuse, status 2 removal, snapshot absence removal, and AD/digital coexistence.
- [ ] **Step 2: Run lifecycle tests and verify they fail before the integration exists.**
- [ ] **Step 3: Implement the virtual department ensure/upsert flow and active member replacement scoped only to the configured department.**
- [ ] **Step 4: Implement OpenIM user registration/update and twin enable/disable while preserving all unrelated `Ex` and twin config fields.**
- [ ] **Step 5: Implement idempotent callback of all current active employees after a successful membership replacement; callback errors are logged and retried by the next scheduled sync.**
- [ ] **Step 6: Start the digital employee scheduler only when config is enabled, using the configured cron, and serialize manual AD+digital execution through the existing sync path.**
- [ ] **Step 7: Run focused lifecycle tests and `go test ./internal/rpc/chat -count=1`.**

### Task 4: Restrict agent search/display and retain the single client sync action

**Files:**
- Modify: `openim-electron-demo/src/utils/agentRecommendations.ts`
- Test: `openim-electron-demo/src/utils/agentRecommendations.test.ts`
- Inspect/modify only if needed: `openim-electron-demo/src/api/organization.ts`, `openim-electron-demo/src/pages/contact/organization/index.tsx`

**Interfaces:**
- `isDisplayableAgent` returns true only for `registerType === REGISTER_TYPE_ADMIN`.
- Existing organization sync continues to call `/ad/sync`; the server-side RPC owns the two-part behavior.

- [ ] **Step 1: Write failing client tests** proving register types 2 is displayed and types 3, 4, and ordinary users are hidden.
- [ ] **Step 2: Run the focused test and verify it fails under the current “exclude only AD” implementation.**
- [ ] **Step 3: Change the predicate to strict equality and add the `REGISTER_TYPE_DIGITAL_EMPLOYEE=4` constant for clarity.**
- [ ] **Step 4: Verify the organization button still calls only `syncADOrganization` and refreshes the department list after success; do not add a second external API call in the browser.**
- [ ] **Step 5: Run the focused test, TypeScript lint, and the client production build.**

### Task 5: QA configuration and verification

**Files:**
- Modify: `config/chat-rpc-chat.yml` and the QA/local deployment config files only if the repository contains their tracked environment-specific equivalents.
- Test: existing Chat and Electron test suites.

- [ ] **Step 1: Add QA/local values only to environment-specific configuration; keep the tracked default disabled and production configuration unchanged.**
- [ ] **Step 2: Run the complete available Chat test suite and client lint/build.**
- [ ] **Step 3: Verify all three repositories’ diffs contain only requested files and document any unavailable toolchain limitation.**

