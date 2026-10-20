// Synthetic-only, normal stock/native and customer UI. No traces/screenshots,
// response text, credentials, tokens or raw exceptions leave this process.
import { createRequire } from 'node:module';

let stage = 'tooling', browser, passed = false;
const ensure = value => { if (!value) throw new Error('closed synthetic assertion'); };
try {
    const url = new URL(process.env.CUSTOMER_SITE_FIXTURE_URL);
    const pin = process.env.CUSTOMER_SITE_FIXTURE_SPKI;
    ensure(url.protocol === 'https:' && url.hostname === '127.0.0.1' && url.port && url.pathname === '/' && !url.search && !url.hash && !url.username && !url.password);
    ensure(/^[A-Za-z0-9+/]{43}=$/.test(pin) && Buffer.from(pin, 'base64').length === 32 && Buffer.from(pin, 'base64').toString('base64') === pin);
    const origin = url.origin;
    const { chromium } = createRequire(import.meta.url)('/Users/mb5/projects/possums/node_modules/playwright-core');
    stage = 'launch';
    browser = await chromium.launch({executablePath:'/Users/mb5/Library/Caches/ms-playwright/chromium-1228/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing', headless:true,
        args:['--ignore-certificate-errors-spki-list='+pin,'--disable-background-networking','--no-proxy-server'],env:{PATH:process.env.PATH || '/usr/bin:/bin'}});
    let external = false, unexpected = false, networkStage = 'privacy_network';
    const unexpectedAt = value => { if (!unexpected) networkStage = value; unexpected = true; };
    const httpErrors = [], consoleErrors = [], contexts = [];
    const newPage = async () => {
        const context = await browser.newContext();
        contexts.push(context);
        await context.route('**/*', route => {
            try {
                if (new URL(route.request().url()).origin === origin) return route.continue();
            } catch { /* refuse malformed destinations without retaining them */ }
            external = true;
            return route.abort();
        });
        const page = await context.newPage();
        page.setDefaultTimeout(15000);
        page.on('pageerror', () => unexpectedAt('privacy_page_error'));
        // CDP supplies a typed cancellation bit. Never guess from errorText.
        // The stock SDK cancels superseded native GETs and unmounted lists.
        // Observe only transient closed kinds; no headers, bodies or URLs retained.
        const network = await context.newCDPSession(page), pendingKinds = new Map();
        network.on('Network.requestWillBeSent', event => {
            let kind = 'privacy_request_failure';
            try {
                const path = new URL(event.request.url).pathname, get = event.request.method === 'GET';
                if (get && path.startsWith('/api/collections/') && path.endsWith('/records')) kind = 'privacy_native_list_failure';
                else if (get && path === '/api/collections') kind = 'privacy_schema_read_failure';
                else if (get && path.includes('/records/')) kind = 'privacy_native_record_failure';
                else if (get && path === '/api/settings') kind = 'privacy_settings_read_failure';
                else if (path === '/api/realtime') kind = 'privacy_realtime_failure';
                else if (event.type === 'Document') kind = 'privacy_navigation_failure';
                else if (event.type === 'Font') kind = 'privacy_font_failure';
                else if (event.type === 'Stylesheet') kind = 'privacy_stylesheet_failure';
            } catch { /* unmatched metadata stays unknown */ }
            pendingKinds.set(event.requestId, kind);
        });
        network.on('Network.loadingFinished', event => pendingKinds.delete(event.requestId));
        network.on('Network.loadingFailed', event => {
            const kind = pendingKinds.get(event.requestId) || 'privacy_request_failure';
            pendingKinds.delete(event.requestId);
            if (event.canceled === true && ['privacy_native_list_failure','privacy_schema_read_failure','privacy_native_record_failure','privacy_settings_read_failure'].includes(kind)) return;
            unexpectedAt(kind);
        });
        await network.send('Network.enable');
        page.on('console', message => {
            if (message.type() === 'warning') unexpectedAt('privacy_console_warning');
            if (message.type() === 'error') {
                try { const u = new URL(message.location().url); ensure(u.origin === origin); consoleErrors.push(u.pathname); }
                catch { unexpectedAt('privacy_console_source'); }
            }
        });
        page.on('response', response => {
            if (response.status() < 400) return;
            try {
                const path = new URL(response.url()).pathname, method = response.request().method();
                const expected = (path === '/logout' && method === 'POST' && response.status() === 409)
                    || (path.endsWith('/records/websitebundle01') && method === 'PATCH' && response.status() === 500);
                if (!expected) unexpectedAt(path === '/favicon.ico' && response.status() === 404 ? 'privacy_favicon_404' : 'privacy_http_response');
                httpErrors.push(path);
            } catch { unexpectedAt('privacy_http_response'); }
        });
        return page;
    };
    const admin = await newPage(), customer = await newPage(), other = await newPage(), publicPage = await newPage();
    const responseAfter = async (page, predicate, action) => {
        // Handle waiter rejection without accepting it as a successful refusal.
        const pending = page.waitForResponse(predicate).then(response => ({response}), () => ({}));
        await action();
        const result = await pending;
        ensure(result.response);
        return result.response;
    };
    stage = 'admin_login';
    await admin.goto(origin+'/_/', {waitUntil:'networkidle'});
    await admin.locator('#login_identity').fill('customer-site@example.invalid');
    await admin.locator('#login_pass').fill('synthetic-customer-site-admin-password');
    await admin.locator('.auth-with-password-form button.next').click();
    await admin.waitForFunction(() => window.app?.pb?.authStore?.isValid && window.app?.store?.collections?.length > 0);
    const provision = async (key, account) => {
        await admin.goto(origin+'/_/#/collections?collection=customer_keys', {waitUntil:'networkidle'});
        await admin.locator('.new-record-btn').click();
        const modal = admin.locator('.record-upsert-modal');
        await modal.locator('input[name="password"]').fill(key);
        await modal.locator('input[name="passwordConfirm"]').fill(key);
        await modal.locator('textarea[name="account_id"]').fill(account);
        const response = await responseAfter(admin, r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/collections/customer_keys/records',
            () => modal.getByRole('button', {name:'Create',exact:true}).click());
        ensure(response.status() === 200);
        await modal.waitFor({state:'hidden'});
        await admin.waitForLoadState('networkidle');
    };
    stage = 'provision';
    await provision('A'.repeat(128), 'synthetic-site-a');
    await provision('C'.repeat(64), 'synthetic-site-b');
    const login = async (page, key) => {
        await page.goto(origin+'/login', {waitUntil:'networkidle'});
        await page.locator('#key').fill(key);
        await page.getByRole('button', {name:'Sign in',exact:true}).click();
        await page.waitForURL(origin+'/home');
        await page.waitForLoadState('networkidle');
    };
    stage = 'customer_login';
    await login(customer, 'A'.repeat(128));
    await login(other, 'C'.repeat(64));
    const snapshot = async () => {
        const text = await customer.locator('main').textContent();
        for (const field of ['synthetic-site-a','Balance (microunits): 100','Available (microunits): 90','Enabled: true','maximum=7','maximum=3','timeout_seconds=600','expires_at=','state="active"','charged_amount=null','terminal_at=null','retain_until=null']) ensure(text.includes(field));
        ensure(text.split('reservation_id=').length === 3 && !text.includes('synthetic-site-b'));
    };
    stage = 'snapshot';
    await snapshot();
    const otherText = await other.locator('main').textContent();
    ensure(otherText.includes('synthetic-site-b') && otherText.includes('Available (microunits): 0') && !otherText.includes('synthetic-site-a') && !otherText.includes('reservation_id='));
    const oldCSS = await customer.locator('link[rel="stylesheet"]').getAttribute('href');
    const editor = () => admin.locator('.field-type-json .code-editor-content');
    const edit = async () => {
        await admin.goto(origin+'/_/#/collections?collection=website_bundles', {waitUntil:'networkidle'});
        await admin.locator('.new-record-btn').waitFor();
        await admin.goto(origin+'/_/#/collections?collection=website_bundles&record=websitebundle01', {waitUntil:'networkidle'});
        await editor().waitFor();
        await admin.waitForFunction(() => document.querySelector('.field-type-json .code-editor-content')?.textContent.includes('init.el'));
    };
    const fill = async value => { await editor().fill(value); await admin.locator('input[name="publish"]').focus(); };
    const save = async () => {
        await admin.locator('input[name="publish"]').focus();
        const response = await responseAfter(admin, r => r.request().method() === 'PATCH' && new URL(r.url()).pathname.endsWith('/records/websitebundle01'),
            () => admin.getByRole('button', {name:'Save changes',exact:true}).click());
        await admin.waitForLoadState('networkidle');
        return response;
    };
    stage = 'draft';
    await edit();
    const original = await editor().textContent(), candidate = JSON.parse(original);
    ensure(candidate.files['pages.el'].includes('Private inference, with clear boundaries.'));
    candidate.files['pages.el'] = candidate.files['pages.el'].replace('Private inference, with clear boundaries.','Synthetic customer publication verified.');
    ensure(candidate.files['style.el'].includes('#fafafa'));
    candidate.files['style.el'] = candidate.files['style.el'].replace('#fafafa','#f8f8f8');
    await fill(JSON.stringify(candidate));
    ensure(!(await admin.locator('input[name="publish"]').isChecked()));
    ensure((await save()).status() === 200);
    await publicPage.goto(origin+'/', {waitUntil:'networkidle'});
    ensure((await publicPage.locator('main').textContent()).includes('Private inference, with clear boundaries.'));
    stage = 'publish';
    await edit();
    await admin.locator('input[name="publish"]').check();
    ensure((await save()).status() === 200);
    await publicPage.reload({waitUntil:'networkidle'});
    ensure((await publicPage.locator('main').textContent()).includes('Synthetic customer publication verified.'));
    const newCSS = await publicPage.locator('link[rel="stylesheet"]').getAttribute('href');
    ensure(newCSS !== oldCSS);
    // Normal browser navigation qualifies the retained previous asset, no fetch/API.
    const cssPage = await newPage();
    ensure((await cssPage.goto(origin+oldCSS, {waitUntil:'networkidle'})).status() === 200);
    stage = 'stale';
    const stale = await responseAfter(customer, r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/logout',
        () => customer.getByRole('button', {name:'Sign out',exact:true}).click());
    ensure(stale.status() === 409);
    await customer.waitForLoadState('networkidle');
    await customer.goto(origin+'/home', {waitUntil:'networkidle'});
    await snapshot();
    stage = 'invalid';
    await edit();
    const invalid = JSON.parse(JSON.stringify(candidate));
    invalid.files['init.el'] = '(';
    await fill(JSON.stringify(invalid));
    await admin.locator('input[name="publish"]').check();
    const refusal = await save();
    // The pinned stock SDK wraps this preparation-hook refusal as HTTP 500;
    // the project's closed body, not status alone, establishes no publication.
    ensure(refusal.status() === 500);
    stage = 'invalid_code';
    ensure((await refusal.json()).code === 'SITE_PUBLICATION_REFUSED');
    await publicPage.reload({waitUntil:'networkidle'});
    ensure((await publicPage.locator('main').textContent()).includes('Synthetic customer publication verified.'));
    stage = 'restore';
    await fill(original);
    await admin.locator('input[name="publish"]').check();
    ensure((await save()).status() === 200);
    await publicPage.reload({waitUntil:'networkidle'});
    ensure((await publicPage.locator('main').textContent()).includes('Private inference, with clear boundaries.'));
    await customer.goto(origin+'/home', {waitUntil:'networkidle'});
    await snapshot();
    stage = 'privacy';
    for (const context of contexts) {
        for (const page of context.pages()) {
            stage = 'privacy_storage';
            ensure(await page.evaluate(async () => localStorage.length === 0 && sessionStorage.length === 0
                && (await indexedDB.databases()).length === 0 && (await caches.keys()).length === 0 && (await navigator.serviceWorker.getRegistrations()).length === 0));
        }
        stage = 'privacy_cookies';
        for (const cookie of await context.cookies()) ensure(['__Host-possums','__Host-possums-csrf'].includes(cookie.name) && cookie.secure && cookie.httpOnly && cookie.sameSite === 'Lax' && cookie.expires === -1 && !cookie.value.includes('.'));
    }
    stage = 'privacy_logging';
    ensure(await admin.evaluate(() => app.store.settings.logs.maxDays === 0));
    stage = external ? 'privacy_external' : networkStage;
    ensure(!external && !unexpected);
    stage = 'privacy_console';
    ensure(httpErrors.length === 2 && consoleErrors.length === httpErrors.length);
    for (const path of consoleErrors) { const i = httpErrors.indexOf(path); ensure(i >= 0); httpErrors.splice(i,1); }
    ensure(httpErrors.length === 0);
    passed = true;
} catch { /* retain only the locally authored stage, never exception text */ }
finally {
    if (browser) {
        try { await browser.close(); }
        catch { if (passed) stage = 'cleanup'; passed = false; }
    }
}
console.log(passed ? 'CUSTOMER_SITE_OK' : 'CUSTOMER_SITE_FAIL '+stage);
process.exitCode = passed ? 0 : 1;
