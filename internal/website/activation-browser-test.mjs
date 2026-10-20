// Explicit synthetic loopback browser acceptance. Uses existing installed tools.
// No screenshots/traces/browser errors/tokens are retained or printed.
import { createRequire } from 'node:module';
import { connect } from 'node:tls';
import { createHash, X509Certificate } from 'node:crypto';

const origin = 'https://[::1]:8444';
const ensure = value => { if (!value) throw new Error('closed fixture assertion'); };
let stage = 'tooling', browser;
let refusalCode='unavailable', refusalStage='unavailable';
try {
    ensure(process.env.PLAYWRIGHT_CORE_PATH && process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH);
    const { chromium } = createRequire(import.meta.url)(process.env.PLAYWRIGHT_CORE_PATH);
    // Acquire only the public, ephemeral local certificate, before credentials.
    // Chrome then accepts that exact SPKI, not a global TLS-verification bypass.
    stage = 'local_certificate';
    const pin = await new Promise((resolve, reject) => {
        const socket = connect({host:'::1',port:8444,rejectUnauthorized:false}, () => {
            try {
                const cert = new X509Certificate(socket.getPeerCertificate().raw);
                ensure(cert.checkIP('::1') && Date.parse(cert.validTo) > Date.now());
                resolve(createHash('sha256').update(cert.publicKey.export({type:'spki',format:'der'})).digest('base64'));
            } catch { reject(new Error('closed certificate acquisition')); }
            socket.end();
        });
        socket.setTimeout(5000, () => socket.destroy());
        socket.on('error', () => reject(new Error('closed local TLS')));
        socket.on('timeout', () => reject(new Error('closed local TLS deadline')));
    });
    stage = 'browser_launch';
    browser = await chromium.launch({executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH,headless:true,args:['--ignore-certificate-errors-spki-list='+pin],env:{PATH:process.env.PATH || '/usr/bin:/bin'}});
    let external = false, pageErrors = false;
    const context = await browser.newContext();
    await context.route('**/*', route => {
        if (new URL(route.request().url()).origin !== origin) { external = true; return route.abort(); }
        return route.continue();
    });
    const admin = await context.newPage(), customer = await context.newPage(), publicPage = await context.newPage();
    for (const page of [admin,customer,publicPage]) page.on('pageerror', () => { pageErrors = true; });
    stage = 'stock_admin_login';
    await admin.goto(origin+'/_/',{waitUntil:'networkidle'});
    await admin.locator('#login_identity').fill('activation-demo@example.invalid');
    await admin.locator('#login_pass').fill('local-activation-demo-password');
    await admin.locator('.auth-with-password-form button.next').click();
    await admin.waitForFunction(() => window.app?.pb?.authStore?.isValid && window.app?.store?.collections?.length > 0);
    const draftEditor = () => admin.locator('.field-type-json .code-editor-content');
    const edit = async () => {
        // Return through the collection list, as a user reopening the row does.
        // Re-navigating to the same hash doesn't necessarily reopen a closed modal.
        await admin.goto(origin+'/_/#/collections?collection=website_bundles',{waitUntil:'networkidle'});
        await admin.locator('.new-record-btn').waitFor();
        await admin.goto(origin+'/_/#/collections?collection=website_bundles&record=websitebundle01',{waitUntil:'networkidle'});
        await draftEditor().waitFor();
        await admin.waitForFunction(() => {
            const editor=document.querySelector('.field-type-json .code-editor-content');
            return editor && editor.textContent.includes('"files"') && editor.textContent.includes('init.el');
        });
    };
    const fillDraft = async value => {
        await draftEditor().fill(value);
        await admin.locator('input[name="publish"]').focus();
    };
    const save = async () => {
        // Stock JSON inputs commit their local editor value on blur.
        await admin.locator('input[name="publish"]').focus();
        const pending = admin.waitForResponse(r => r.request().method()==='PATCH' && new URL(r.url()).pathname.endsWith('/records/websitebundle01')).catch(() => null);
        await admin.getByRole('button',{name:'Save changes',exact:true}).click();
        const response = await pending;
        ensure(response);
        return response;
    };
    stage = 'stock_draft_edit';
    await edit();
    stage='initial_draft_baseline';
    const initial = await draftEditor().textContent();
    const original = initial.replace('Synthetic live publication verified.','Private inference, with clear boundaries.');
    ensure(original.includes('Private inference, with clear boundaries.'));
    if (initial!==original) {
        // Deliberate code-only fixture setup after inspecting an earlier stopped
        // test's known marker, not retry/recovery of an unknown publication.
        stage='restore_known_synthetic_baseline';
        await fillDraft(original);
        await admin.locator('input[name="publish"]').check();
        ensure((await save()).status()===200);
        await edit();
    }
    stage='draft_inactive_save';
    const candidate = original.replace('Private inference, with clear boundaries.','Synthetic live publication verified.');
    await fillDraft(candidate);
    ensure(!(await admin.locator('input[name="publish"]').isChecked()));
    ensure((await save()).status()===200);
    await publicPage.goto(origin+'/',{waitUntil:'networkidle'});
    ensure((await publicPage.locator('main').textContent()).includes('Private inference, with clear boundaries.'));
    stage = 'customer_login';
    await customer.goto(origin+'/login',{waitUntil:'networkidle'});
    await customer.locator('#key').fill('local-demo-key');
    await customer.getByRole('button',{name:'Sign in',exact:true}).click();
    await customer.waitForURL(origin+'/home');
    ensure(await customer.locator('main').textContent().then(t => t.includes('You are signed in.')));
    stage = 'stock_publish_checkbox';
    await edit();
    await admin.locator('input[name="publish"]').check();
    ensure((await save()).status()===200);
    await publicPage.reload({waitUntil:'networkidle'});
    ensure((await publicPage.locator('main').textContent()).includes('Synthetic live publication verified.'));
    stage = 'stale_form_session_continuity';
    const stale = customer.waitForResponse(r => new URL(r.url()).pathname==='/logout' && r.request().method()==='POST').catch(() => null);
    await customer.getByRole('button',{name:'Sign out',exact:true}).click();
    const staleResponse=await stale;
    ensure(staleResponse && staleResponse.status()===409);
    await customer.goto(origin+'/home',{waitUntil:'networkidle'});
    ensure((await customer.locator('main').textContent()).includes('You are signed in.'));
    stage = 'invalid_candidate_editor';
    await edit();
    stage='invalid_candidate_json_parse';
    const invalid = JSON.parse(candidate); invalid.files['init.el'] = '(';
    stage='invalid_candidate_fill';
    await fillDraft(JSON.stringify(invalid));
    stage='invalid_candidate_toggle';
    await admin.locator('input[name="publish"]').check();
    stage='invalid_candidate_submit';
    const refused = await save();
    stage='invalid_candidate_response';
    const diagnostic=await refused.json();
    if (['SITE_PUBLICATION_REFUSED','SITE_PUBLICATION_UNAVAILABLE','OPERATOR_NATIVE_REQUEST_UNAVAILABLE'].includes(diagnostic.code)) refusalCode=diagnostic.code;
    if (['publication_prepare','publication_dispatch','publication_commit','native_request'].includes(diagnostic.stage)) refusalStage=diagnostic.stage;
    ensure(refused.status()!==200 && diagnostic.code==='SITE_PUBLICATION_REFUSED');
    stage='invalid_candidate_active_page';
    await publicPage.reload({waitUntil:'networkidle'});
    ensure((await publicPage.locator('main').textContent()).includes('Synthetic live publication verified.'));
    stage = 'restore_baseline_through_stock_editor';
    await fillDraft(original);
    await admin.locator('input[name="publish"]').check();
    ensure((await save()).status()===200);
    await publicPage.reload({waitUntil:'networkidle'});
    ensure((await publicPage.locator('main').textContent()).includes('Private inference, with clear boundaries.'));
    stage = 'browser_privacy';
    const clean = await admin.evaluate(() => localStorage.length===0 && sessionStorage.length===0 && app.store.settings.logs.maxDays===0);
    ensure(clean && !external && !pageErrors);
    console.log('PASS activation-browser: stock_editor_draft_publish_invalid_restore_session_stale_form_local_privacy');
} catch {
    console.error('ACTIVATION_BROWSER_UNAVAILABLE stage='+stage+' refusal_code='+refusalCode+' refusal_stage='+refusalStage+' cause=unknown; no automatic replay; inspect synthetic state privately');
    process.exitCode = 1;
} finally {
    if (browser) {
        try { await browser.close(); }
        catch { console.error('ACTIVATION_BROWSER_CLEANUP_UNCONFIRMED cause=unknown'); process.exitCode=1; }
    }
}
