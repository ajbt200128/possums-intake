// Explicit synthetic loopback acceptance for the stock PocketBase native editor.
// Uses only the already installed playwright-core + Chromium; installs nothing.
// The parent Go test supplies the ephemeral fixture origin and its public SPKI.
// Never retained or printed: URLs, tokens, page text, input values, screenshots,
// traces, raw exceptions. Failures are closed stage-specific codes only.
import { createRequire } from 'node:module';

const corePath = process.env.PLAYWRIGHT_CORE_PATH || '/Users/mb5/projects/possums/node_modules/playwright-core';
const collection = 'accounts';
const admin = { identity: 'key-fixture@example.invalid', password: 'synthetic-native-key-password' };
// Deterministic public synthetic material; never a real gateway key.
const key = 'A'.repeat(128);
const otherKey = 'B'.repeat(64);
const otherConfirm = 'C'.repeat(64);
const thirdKey = 'D'.repeat(64);
const loopbackHosts = new Set(['127.0.0.1', '[::1]', '::1', 'localhost']);

const ensure = (value) => { if (!value) throw new Error('closed synthetic assertion'); };
const constraints = {
    fixture_inputs: 'public_fixture_origin_and_pinned_spki',
    browser_launch: 'installed_chromium_launch',
    browser_context: 'isolated_browser_context_navigation',
    persistence: 'page_memory_only_storage',
    route_policy_external: 'fixture_origin_only',
    route_policy_page_errors: 'no_page_exceptions',
    route_policy_console_errors: 'no_unexpected_console_errors',
    route_policy_console_warnings: 'no_console_warnings',
    route_policy_refusal_errors: 'matched_expected_resource_errors',
    route_policy_request_failures: 'no_fixture_request_failure',
    route_policy_http_errors: 'no_unexpected_http_failure',
};
const constraintFor = (current) => constraints[current] || 'stock_native_editor_flow';
const parse = (text) => {
    // Hostile or unexpected bytes are refused as a closed failure, never echoed.
    try { return { value: new URL(text) }; } catch { return null; }
};
const decode = (text) => {
    try { return { value: JSON.parse(text) }; } catch { return null; }
};

let stage = 'fixture_inputs';
let browser;

function fixture() {
    // Only an exact https loopback origin is accepted; nothing else is reachable.
    const raw = process.env.CUSTOMER_KEY_FIXTURE_URL || '';
    const spki = process.env.CUSTOMER_KEY_FIXTURE_SPKI || '';
    ensure(raw && /^[A-Za-z0-9+/]{43}=$/.test(spki));
    ensure(Buffer.from(spki, 'base64').length === 32);
    const parsed = parse(raw);
    ensure(parsed);
    const url = parsed.value;
    ensure(url.protocol === 'https:' && loopbackHosts.has(url.hostname));
    ensure(!url.username && !url.password && !url.search && !url.hash && (url.pathname === '' || url.pathname === '/'));
    return { origin: url.origin, spki };
}

try {
    const { origin, spki } = fixture();
    const { chromium, errors } = createRequire(import.meta.url)(corePath);

    stage = 'browser_launch';
    // Chrome accepts exactly this SPKI; no blanket TLS-verification bypass.
    // A minimal environment keeps runtime credentials and proxy settings out of
    // the browser. The route guard below constrains page-controlled requests.
    const env = { PATH: process.env.PATH || '/usr/bin:/bin' };
    if (process.env.HOME) env.HOME = process.env.HOME;
    browser = await chromium.launch({ executablePath: '/Users/mb5/Library/Caches/ms-playwright/chromium-1228/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing', headless: true, env, args: ['--ignore-certificate-errors-spki-list=' + spki] });

    stage = 'browser_context';
    let external = false, pageErrors = 0, consoleErrors = 0, consoleWarnings = 0, requestFailures = 0, unexpectedResponses = 0, expectedRefusals = 0, refusalResourceErrors = 0;
    let refusing = false;
    const expectedRefusal = (response) => {
        const parsed = parse(response.url());
        return refusing && response.request().method() === 'POST' && parsed !== null
            && parsed.value.pathname === '/api/collections/' + collection + '/records' && response.status() === 400;
    };
    const context = await browser.newContext();
    context.setDefaultTimeout(15000);
    await context.route('**/*', (route) => {
        const target = route.request().url();
        if (/^(?:data|blob|about):/.test(target)) return route.continue();
        const parsed = parse(target);
        if (parsed && parsed.value.protocol === 'https:' && parsed.value.origin === origin) return route.continue();
        external = true;
        return route.abort();
    });
    const page = await context.newPage();
    page.on('pageerror', () => { pageErrors++; });
    page.on('console', (message) => {
        // Use type/location metadata only, never free-form console text. Each
        // expected HTTP refusal must have its own matching Chromium resource error.
        if (message.type() === 'error') {
            const location = parse(message.location().url);
            // Console delivery may lag the matching response. Match the source
            // here and the exact expected HTTP-error population at final admission.
            if (location && location.value.origin === origin
                && location.value.pathname === '/api/collections/' + collection + '/records') refusalResourceErrors++;
            else consoleErrors++;
        }
        if (message.type() === 'warning') consoleWarnings++;
    });
    page.on('response', (response) => {
        const parsed = parse(response.url());
        if (!parsed || parsed.value.origin !== origin) return;
        if (response.status() >= 400) {
            if (expectedRefusal(response)) expectedRefusals++;
            else unexpectedResponses++;
        }
    });
    page.on('requestfailed', (request) => {
        const parsed = parse(request.url());
        if (parsed && parsed.value.origin === origin) requestFailures++;
    });

    const modal = page.locator('.record-upsert-modal');
    const openEditor = async () => {
        await page.locator('.new-record-btn').click();
        await modal.locator('input[name="password"]').waitFor();
        ensure(await modal.locator('[name="account_id"]').count() === 0);
        await modal.locator('input[name="enabled"]').check();
    };
    const submit = async (timeout = 15000) => {
        const pending = page.waitForResponse((response) => {
            const parsed = parse(response.url());
            return response.request().method() === 'POST'
                && parsed !== null && parsed.value.pathname === '/api/collections/' + collection + '/records';
        }, { timeout }).then(response => ({ response }), error => ({ timeout: error instanceof errors.TimeoutError }));
        // An unexpected click/tool failure is not evidence of client refusal.
        await modal.getByRole('button', { name: 'Create', exact: true }).click();
        const result = await pending;
        ensure(result.response || result.timeout);
        return result.response || null;
    };
    const attemptRefusal = async () => {
        // Stock required-field validation may refuse before any request is sent.
        refusing = true;
        const response = await submit(2000);
        ensure(!response || response.status() === 400);
        await page.waitForLoadState('networkidle');
        refusing = false;
        ensure(await modal.isVisible());
    };
    const savedAccountRows = () => page.locator('.records-table tbody .col-field-name-id').count();

    stage = 'stock_login';
    await page.goto(origin + '/_/', { waitUntil: 'networkidle' });
    await page.locator('#login_identity').fill(admin.identity);
    await page.locator('#login_pass').fill(admin.password);
    await page.locator('.auth-with-password-form button.next').click();
    await page.waitForFunction(() => window.app?.pb?.authStore?.isValid && window.app?.store?.collections?.length > 0);

    stage = 'collection_open';
    await page.goto(origin + '/_/#/collections?collection=' + collection, { waitUntil: 'networkidle' });
    await page.waitForFunction((name) => window.app?.store?.activeCollection?.name === name, collection);
    await page.locator('.new-record-btn').waitFor();

    stage = 'editor_fill';
    await openEditor();
    // Only the ordinary stock inputs: no lookup, verifier, or generated password.
    await modal.locator('input[name="password"]').fill(key);
    await modal.locator('input[name="passwordConfirm"]').fill(key);

    stage = 'create_submit';
    const created = await submit();
    stage = 'create_response';
    ensure(created.status() === 200);
    const body = await created.text();
    ensure(!body.includes(key) && !body.includes(otherKey) && !body.includes(thirdKey));
    const decoded = decode(body);
    ensure(decoded && /^[a-z0-9]{15}$/.test(decoded.value.id) && !('account_id' in decoded.value)
        && decoded.value.balance === '0' && decoded.value.enabled === true);

    stage = 'create_listed';
    await page.waitForFunction((expected) => {
        for (const cell of document.querySelectorAll('.records-table tbody .col-field-name-id')) {
            if ((cell.textContent || '').includes(expected)) return true;
        }
        return false;
    }, decoded.value.id);

    stage = 'mismatch_fill';
    await openEditor();
    await modal.locator('input[name="password"]').fill(otherKey);
    await modal.locator('input[name="passwordConfirm"]').fill(otherConfirm);

    stage = 'mismatch_submit';
    await attemptRefusal();

    stage = 'missing_fill';
    await modal.locator('input[name="password"]').fill(thirdKey);
    await modal.locator('input[name="passwordConfirm"]').fill('');

    stage = 'missing_submit';
    await attemptRefusal();

    stage = 'editor_close';
    await modal.getByRole('button', { name: 'Close', exact: true }).first().click();
    const discard = page.locator('.modal.popup').getByRole('button', { name: 'Yes, discard', exact: true });
    await discard.waitFor({ timeout: 2000 }).catch(() => null);
    if (await discard.isVisible()) await discard.click();
    await modal.waitFor({ state: 'detached' });

    stage = 'refusals_saved_nothing';
    ensure((await savedAccountRows()) === 1);

    stage = 'route_policy_external'; ensure(!external);
    stage = 'route_policy_page_errors'; ensure(pageErrors === 0);
    stage = 'route_policy_console_errors'; ensure(consoleErrors === 0);
    stage = 'route_policy_console_warnings'; ensure(consoleWarnings === 0);
    stage = 'route_policy_refusal_errors'; ensure(refusalResourceErrors === expectedRefusals);
    stage = 'route_policy_request_failures'; ensure(requestFailures === 0);
    stage = 'route_policy_http_errors'; ensure(unexpectedResponses === 0);

    stage = 'persistence';
    const state = await page.evaluate(async () => {
        const databases = await indexedDB.databases();
        const cacheKeys = await caches.keys();
        const registrations = await navigator.serviceWorker.getRegistrations();
        return {
            local: localStorage.length,
            session: sessionStorage.length,
            databases: databases.length,
            caches: cacheKeys.length,
            workers: registrations.length,
        };
    });
    ensure(state.local === 0 && state.session === 0 && state.databases === 0 && state.caches === 0 && state.workers === 0);
    ensure((await context.cookies()).length === 0);

    stage = 'done';
    await browser.close();
    browser = undefined;
    console.log('CUSTOMER_NATIVE_EDITOR_OK');
} catch {
    process.stderr.write('CUSTOMER_NATIVE_EDITOR_UNAVAILABLE stage=' + stage + ' constraint=' + constraintFor(stage)
        + ' cause=unknown; inspect the isolated synthetic fixture privately; no automatic retry and no acceptance claimed\n');
    process.exitCode = 1;
} finally {
    if (browser) {
        try { await browser.close(); }
        catch { process.stderr.write('CUSTOMER_NATIVE_EDITOR_CLEANUP_UNCONFIRMED cause=unknown\n'); process.exitCode = 1; }
    }
}
