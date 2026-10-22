// Isolated synthetic compiled-main/browser qualification; no tool installation.
// Supply INTAKE_BINARY, PLAYWRIGHT_CORE_PATH and PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH.
import { createRequire } from "node:module";
import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { createServer } from "node:net";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const email = "synthetic@example.invalid";
const password = "SYNTHETIC_BROWSER_PASSWORD_ONLY_123";
const key = "SYNTHETIC_BROWSER_ACCOUNTING_KEY_32_BYTES";
const sentinel = "SYNTHETIC_PRIVATE_SENTINEL";
let stage = "tooling";
let service, serviceClosed, browser, directory;
let output = "", stderr = "";
let nativeReadFailures = [];
let uiState = {};
let diagnosticPage;
const ensure = (value) => { if (!value) throw new Error("closed synthetic assertion"); };
const env = { PATH: process.env.PATH || "/usr/bin:/bin", OTEL_SDK_DISABLED: "true", LEDGER_ADMIN_KEY: key };
const bounded = (previous, bytes) => (previous + bytes).slice(0, 65537);
function run(args, extra = {}) {
    return new Promise((resolve, reject) => {
        const child = spawn(process.env.INTAKE_BINARY, args, { env: { ...env, ...extra }, stdio: ["ignore", "pipe", "pipe"] });
        let out = "", error = "";
        child.stdout.on("data", bytes => { out = bounded(out, bytes); });
        child.stderr.on("data", bytes => { error = bounded(error, bytes); });
        child.on("error", () => reject(new Error("closed local process unavailable")));
        child.on("close", code => resolve({ code, out, error }));
    });
}
function inspectPrivacyState(state) {
    // Read-only test inspection after actual service join; no admin/log API,
    // runtime helper, automatic diagnostic log or second spendable authority.
    const script = 'import sqlite3,sys,json\ntry:\n result={}\n for file,table,label in [("auxiliary.db","_logs","logs_empty"),("data.db","_authOrigins","origins_empty")]:\n  db=sqlite3.connect("file:"+sys.argv[1]+"/"+file+"?mode=ro",uri=True)\n  result[label]=db.execute("SELECT count(*) FROM "+table).fetchone()[0]==0\n  db.close()\n print(json.dumps(result))\nexcept Exception:\n print("OPERATOR_BROWSER_STATE_INSPECTION_UNAVAILABLE")\n sys.exit(1)\n';
    return new Promise((resolve, reject) => {
        const child = spawn("python3", ["-c", script, state], { env, stdio: ["ignore", "pipe", "pipe"] });
        let out = "", error = "";
        child.stdout.on("data", bytes => { out = bounded(out, bytes); });
        child.stderr.on("data", bytes => { error = bounded(error, bytes); });
        child.once("error", () => reject(new Error("closed privacy inspection unavailable")));
        child.once("close", code => {
            try { const result = JSON.parse(out); ensure(code === 0 && error === "" && result.logs_empty && result.origins_empty); resolve(); }
            catch { reject(new Error("closed privacy state assertion")); }
        });
    });
}
function wait(ms) { return new Promise(resolve => setTimeout(resolve, ms)); }
async function port() {
    const listener = createServer();
    await new Promise((resolve, reject) => { listener.once("error", () => reject(new Error("closed loopback unavailable"))); listener.listen(0, "127.0.0.1", resolve); });
    const result = listener.address().port;
    await new Promise(resolve => listener.close(resolve));
    return result;
}
try {
    ensure(process.env.INTAKE_BINARY && process.env.PLAYWRIGHT_CORE_PATH && process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH);
    const { chromium } = createRequire(import.meta.url)(process.env.PLAYWRIGHT_CORE_PATH);
    directory = await mkdtemp(path.join(root, ".operator-browser-test-"));
    const state = path.join(directory, "state");
    stage = "explicit_init";
    const initialized = await run(["init", "--operator", "--state", state], { LEDGER_OPERATOR_EMAIL: email, LEDGER_OPERATOR_PASSWORD: password });
    ensure(initialized.code === 0 && initialized.error === "" && initialized.out === '{"status":"initialized_empty"}\n');
    const address = "127.0.0.1:" + await port();
    const origin = "http://" + address;
    stage = "compiled_serve";
    service = spawn(process.env.INTAKE_BINARY, ["serve", "--operator", "--state", state, "--listen", address], { env, stdio: ["ignore", "pipe", "pipe"] });
    serviceClosed = new Promise(resolve => service.once("close", resolve));
    service.stdout.on("data", bytes => { output = bounded(output, bytes); });
    service.stderr.on("data", bytes => { stderr = bounded(stderr, bytes); });
    service.on("error", () => { stderr = "closed process unavailable"; });
    let ready = false;
    for (let attempt = 0; attempt < 100; attempt++) {
        try { ready = (await fetch(origin + "/healthz")).status === 200; } catch { /* startup not ready */ }
        if (ready) break;
        await wait(50);
    }
    ensure(ready);
    stage = "browser_launch";
    browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH, headless: true, env: { PATH: env.PATH } });
    const context = await browser.newContext();
    const page = await context.newPage();
    diagnosticPage = page;
    let external = false, leaked = false, pageErrors = 0, fontLoaded = false, fontFailed = false;
    await context.route("**/*", route => {
        if (new URL(route.request().url()).origin !== origin) { external = true; return route.abort(); }
        return route.continue();
    });
    page.on("pageerror", () => { pageErrors++; });
    page.on("response", response => {
        const url = new URL(response.url());
        if (/\.(?:woff2?|ttf)$/.test(url.pathname)) {
            fontLoaded ||= response.status() === 200;
            fontFailed ||= response.status() >= 400;
        }
        if (url.pathname.startsWith("/api/") && response.status() >= 400 && nativeReadFailures.length < 12) {
            const component = url.pathname === "/api/collections" ? "collection_list" : url.pathname.endsWith("/records") ? "record_list" : url.pathname === "/api/settings" ? "settings" : "other_native";
            nativeReadFailures.push({ component, status: response.status(), query_keys: [...url.searchParams.keys()].filter(key => ["page", "perPage", "skipTotal", "filter", "sort", "fields", "expand"].includes(key)), pagination: ["page", "perPage", "skipTotal"].map(key => { const v = url.searchParams.get(key); return /^(?:[0-9]{1,4}|true|false)$/.test(v || "") ? v : "unavailable"; }) });
        }
    });
    page.on("console", event => { if (event.text().includes(sentinel) || event.text().includes(password) || event.text().includes(key)) leaked = true; });
    await page.addInitScript(() => {
        window.syntheticCSPFailures = 0;
        window.addEventListener("securitypolicyviolation", () => { window.syntheticCSPFailures++; });
    });
    stage = "native_ui_login";
    await page.goto(origin + "/_/", { waitUntil: "networkidle" });
    stage = "native_ui_login_fields";
    await page.locator("#login_identity").fill(email);
    await page.locator("#login_pass").fill(sentinel);
    await page.locator(".auth-with-password-form button.next").click();
    stage = "native_ui_closed_login_rejection";
    await page.waitForFunction(() => document.body.textContent.includes("OPERATOR_UI_REQUEST_UNAVAILABLE"));
    ensure(!leaked);
    await page.locator("#login_pass").fill(password);
    await page.locator(".auth-with-password-form button.next").click();
    nativeReadFailures = [];
    stage = "native_ui_authenticated_initial_reads";
    await page.waitForFunction(() => window.app?.pb?.authStore?.isValid && window.app?.store?.settings?.meta && window.app?.store?.collections?.length > 0 && !window.app.store.isLoadingCollections);
    await page.waitForLoadState("networkidle");
    stage = "native_ui_trusted_admin_notice_and_fonts";
    ensure(await page.evaluate(() => document.body.textContent.includes("Stock writes bypass ledger safeguards")));
    ensure(fontLoaded && !fontFailed);
    stage = "native_ui_account_editor_create";
    const accounting = async (path, body, extra = {}) => {
        const response = await fetch(origin + path, { method: "POST", headers: { "Content-Type": "application/json", Authorization: "Bearer " + key, ...extra }, body: JSON.stringify(body) });
        return { status: response.status, body: await response.json() };
    };
    const customerKey = "A".repeat(64);
    await page.goto(origin + "/_/#/collections?collection=accounts", { waitUntil: "networkidle" });
    stage = "native_ui_account_collection_ready";
    await page.waitForFunction(() => app.store.activeCollection?.name === "accounts");
    stage = "native_ui_new_account_modal";
    await page.locator(".new-record-btn").click();
    stage = "native_ui_fill_new_account";
    ensure(await page.locator('[name="account_id"]').count() === 0);
    await page.locator('input[name="password"]').fill(customerKey);
    await page.locator('input[name="passwordConfirm"]').fill(customerKey);
    await page.locator('textarea[name="balance"]').fill("100");
    await page.locator('input[name="enabled"]').check();
    const createdResponse = page.waitForResponse(r => r.request().method() === "POST" && new URL(r.url()).pathname === "/api/collections/accounts/records");
    stage = "native_ui_submit_account_create";
    await page.getByRole("button", { name: "Create", exact: true }).click();
    const created = await createdResponse;
    stage = "native_ui_account_create_response";
    ensure(created.status() === 200);
    const nativeRecord = await created.json();
    ensure(!Object.hasOwn(nativeRecord, "account_id") && nativeRecord.balance === "100" && nativeRecord.enabled === true && /^[a-z0-9]{15}$/.test(nativeRecord.id));
    const accountID = nativeRecord.id;
    await page.locator('textarea[name="balance"]').waitFor({ state: "detached" });
    ensure((await accounting("/balance", { account_id: accountID })).body.available === "100");
    const openEditor = async () => {
        await page.goto(origin + "/_/#/collections?collection=accounts&record=" + nativeRecord.id, { waitUntil: "networkidle" });
        await page.locator('textarea[name="balance"]').waitFor();
    };
    const saveEditor = async () => {
        const pending = page.waitForResponse(r => r.request().method() === "PATCH" && new URL(r.url()).pathname.endsWith("/records/" + nativeRecord.id));
        await page.getByRole("button", { name: "Save changes", exact: true }).click();
        return pending;
    };
    stage = "native_ui_account_editor_edit";
    await openEditor();
    await page.locator('textarea[name="balance"]').fill("90");
    ensure((await saveEditor()).status() === 200);
    await page.locator('textarea[name="balance"]').waitFor({ state: "detached" });
    ensure((await accounting("/balance", { account_id: accountID })).body.available === "90");
    stage = "native_ui_trusted_active_hold_balance_edit";
    const reservation = await accounting("/reservations", { account_id: accountID, maximum: "20", timeout_seconds: 600 }, { "Idempotency-Key": "v1." + Date.now() + "." + "1".repeat(32) });
    ensure(reservation.status === 200);
    await openEditor();
    await page.locator('textarea[name="balance"]').fill("80");
    ensure((await saveEditor()).status() === 200);
    await page.locator('textarea[name="balance"]').waitFor({ state: "detached" });
    ensure((await accounting("/balance", { account_id: accountID })).body.available === "60");
    stage = "native_ui_stock_enabled_edit_and_stale_balance";
    await openEditor();
    await page.locator('input[name="enabled"]').uncheck();
    ensure((await saveEditor()).status() === 200);
    await page.locator('textarea[name="balance"]').waitFor({ state: "detached" });
    await openEditor(); // stock absolute editor intentionally permits stale overwrite
    ensure((await accounting("/credits", { account_id: accountID, amount: "5" }, { "Idempotency-Key": "v1." + Date.now() + "." + "2".repeat(32) })).status === 200);
    ensure((await accounting("/reservations/settle", { reservation_id: reservation.body.reservation.reservation_id, charged_amount: "10" })).status === 200);
    await page.locator('input[name="enabled"]').check();
    const enabledResponse = await saveEditor();
    ensure(enabledResponse.status() === 200);
    const enabledRequest = enabledResponse.request().postDataJSON();
    ensure(Object.hasOwn(enabledRequest, "balance") && !Object.hasOwn(enabledRequest, "account_id"));
    const snapshot = await accounting("/balance", { account_id: accountID });
    ensure(snapshot.body.account_balance[0].balance === "80" && snapshot.body.available === "80" && snapshot.body.pending_reservations.length === 0);
    stage = "native_ui_token_key_separation";
    const token = await page.evaluate(() => app.pb.authStore.token);
    ensure((await fetch(origin + "/balance", { method: "POST", headers: { "Content-Type": "application/json", Authorization: "Bearer " + token }, body: JSON.stringify({ account_id: accountID }) })).status === 401);
    const byID = await page.evaluate(async id => app.pb.collection("ledgeraccounts2").getOne(id), nativeRecord.id);
    ensure(byID.balance === "80");
    stage = "native_ui_refresh_storage_csp";
    const native = await page.evaluate(async () => {
        const headers = { Authorization: app.pb.authStore.token };
        const refreshed = await app.pb.collection("_superusers").authRefresh();
        const statuses = await Promise.all(["/api/settings", "/api/collections", "/api/collections/meta/scaffolds", "/api/collections/meta/oauth2-providers", "/api/collections/_superusers/records"].map(url => fetch(url, { headers }).then(r => r.status)));
        return { refreshed: !!refreshed.token, statuses, storage: [localStorage.length, sessionStorage.length], csp: window.syntheticCSPFailures };
    });
    ensure(native.refreshed && native.statuses.every(status => status === 200) && native.storage.every(n => n === 0) && native.csp === 0);
    stage = "native_ui_logs_disabled";
    ensure(await page.evaluate(() => !!document.querySelector('a[href="#/logs"]') && app.store.settings.logs?.maxDays === 0));
    const loggingStatuses = await page.evaluate(async () => {
        const headers = { Authorization: app.pb.authStore.token, "Content-Type": "application/json" };
        return Promise.all([
            fetch("/api/logs?superuserRequests=0", { headers }).then(r => r.status),
            fetch("/api/logs/stats", { headers }).then(r => r.status),
            fetch("/api/settings", { method: "PATCH", headers, body: JSON.stringify({ logs: { maxDays: 5 } }) }).then(r => r.status),
        ]);
    });
    ensure(loggingStatuses.every(status => status === 200));
    stage = "native_ui_users_and_schema_controls";
    await page.goto(origin + "/_/#/collections?collection=users", { waitUntil: "networkidle" });
    await page.waitForFunction(() => app.store.activeCollection?.name === "users");
    ensure(await page.locator(".new-record-btn").isVisible());
    ensure(await page.locator(".btn-collection-settings").isVisible());
    stage = "native_ui_backup_controls";
    await page.goto(origin + "/_/#/settings/backups", { waitUntil: "networkidle" });
    await page.locator(".page-backups-settings").waitFor();
    stage = "native_ui_logs_page";
    await page.goto(origin + "/_/#/logs?superuserRequests=0", { waitUntil: "networkidle" });
    await page.locator(".page-logs").waitFor();
    await page.reload({ waitUntil: "networkidle" });
    await page.locator("#login_identity").waitFor();
    ensure(await page.evaluate(() => !app.pb.authStore.token && localStorage.length === 0 && sessionStorage.length === 0));
    ensure((await context.cookies()).length === 0);
    ensure(!external && !leaked && pageErrors === 0);
    stage = "shutdown_stdio";
    await browser.close(); browser = undefined;
    service.kill("SIGTERM");
    ensure(await serviceClosed === 0);
    service = undefined;
    ensure(output === "" && stderr === "");
    stage = "operator_joined_state_privacy";
    await inspectPrivacyState(state);
    stage = "default_surface_logging_disabled";
    output = ""; stderr = "";
    service = spawn(process.env.INTAKE_BINARY, ["serve", "--state", state, "--listen", address], { env, stdio: ["ignore", "pipe", "pipe"] });
    serviceClosed = new Promise(resolve => service.once("close", resolve));
    service.stdout.on("data", bytes => { output = bounded(output, bytes); });
    service.stderr.on("data", bytes => { stderr = bounded(stderr, bytes); });
    service.on("error", () => { stderr = "closed default process unavailable"; });
    ready = false;
    for (let attempt = 0; attempt < 100; attempt++) {
        try { ready = (await fetch(origin + "/healthz")).status === 200; } catch { /* default startup not ready */ }
        if (ready) break;
        await wait(50);
    }
    ensure(ready);
    ensure((await fetch(origin + "/api/logs?superuserRequests=0")).status === 404);
    ensure((await fetch(origin + "/api/logs/stats")).status === 404);
    ensure((await fetch(origin + "/_/")).status === 404);
    const defaultFailure = await fetch(origin + "/balance", { method: "POST", headers: { "Content-Type": "application/json", Authorization: "Bearer " + sentinel }, body: JSON.stringify({ account_id: sentinel }) });
    ensure(defaultFailure.status === 401 && !(await defaultFailure.text()).includes(sentinel));
    service.kill("SIGTERM");
    ensure(await serviceClosed === 0);
    service = undefined;
    ensure(output === "" && stderr === "");
    stage = "default_joined_state_privacy";
    await inspectPrivacyState(state);
    process.stdout.write('{"status":"operator_browser_pass","scope":"compiled_main_isolated_synthetic_chromium"}\n');
} catch {
    if (diagnosticPage) {
        try { uiState = await diagnosticPage.evaluate(() => ({ ready: !!window.app?.store?._ready, collections: !!window.app?.store?.collections?.length, active_account: window.app?.store?.activeCollection?.name === "accounts", active_id: !!window.app?.store?.activeCollection?.id, button_hidden_ancestor: !!document.querySelector(".new-record-btn")?.closest("[hidden]"), hidden_primary_buttons: !!document.querySelector(".page-header-primary-btns")?.hasAttribute("hidden"), hidden_page: !!document.querySelector(".new-record-btn")?.closest(".page[hidden]"), hidden_app: !!document.querySelector(".app")?.hasAttribute("hidden"), body_hidden: getComputedStyle(document.body).display === "none", new_buttons: document.querySelectorAll(".new-record-btn").length, button_visible: !!document.querySelector(".new-record-btn")?.offsetParent, button_enabled: !document.querySelector(".new-record-btn")?.disabled, button_unobstructed: (() => { const button = document.querySelector(".new-record-btn"); if (!button) return false; const r = button.getBoundingClientRect(); return button.contains(document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2)); })(), loading: !!window.app?.store?.isLoadingCollections, csp: window.syntheticCSPFailures })); } catch { /* closed diagnostic unavailable */ }
    }
    process.stderr.write(JSON.stringify({ code: "OPERATOR_BROWSER_TEST_UNAVAILABLE", stage, constraint: "synthetic_browser_assertion_or_tool_boundary", native_read_failures: nativeReadFailures, ui_state: uiState, next_step: "Inspect locally; no raw errors captured or acceptance claimed." }) + "\n");
    process.exitCode = 1;
} finally {
    let joined = true;
    try { if (browser) await browser.close(); } catch { joined = false; }
    try {
        if (service) {
            if (service.exitCode === null && service.signalCode === null) service.kill("SIGTERM");
            await serviceClosed; // actual close/pipe join, including already-exited children
        }
    } catch { joined = false; }
    if (joined && directory) {
        try { await rm(directory, { recursive: true, force: true }); } catch { joined = false; }
    }
    if (!joined) {
        // Preserve disposable state when cleanup/join isn't observed. Never emit
        // browser/OS exceptions or turn an unknown join into permission to erase.
        process.stderr.write('{"code":"OPERATOR_BROWSER_CLEANUP_UNAVAILABLE","stage":"cleanup","constraint":"observed_browser_and_service_join_before_state_removal","next_step":"Inspect retained disposable state and processes privately."}\n');
        process.exitCode = 1;
    }
}
