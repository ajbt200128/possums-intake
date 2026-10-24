// Isolated local fixture only; no screenshots, traces, raw exceptions or payload export.
import { createRequire } from 'node:module';
let stage = 'configuration', browser, passed = false;
const ensure = value => { if (!value) throw new Error('closed fixture assertion'); };
try {
    const account = new URL(process.env.TOPUP_ACCOUNT_ORIGIN);
    const checkout = new URL(process.env.TOPUP_CHECKOUT_ORIGIN);
    const pin = process.env.TOPUP_ACCOUNT_SPKI;
    const mode = process.env.TOPUP_PAYMENT_MODE;
    ensure(mode === 'live' || mode === 'test');
    const checkNotice = text => {
        ensure(text.includes('Sandbox/test only. Use synthetic payment details; do not make real payments.') === (mode === 'test'));
        for (const notice of [
            'Payments are live. The selected USD amount becomes inference credit; tax is added at Stripe Checkout and does not become credit.',
            'Stripe processes payment and tax details and keeps its own records. Card and tax-address details are not stored in our local sales records.',
            'Local purchase links and sales records share a deletion cutoff 20 days after checkout starts. Maintenance delays and storage remnants can retain data longer; this does not delete Stripe records.',
        ]) ensure(text.includes(notice) === (mode === 'live'));
        if (mode === 'live') ensure(!text.toLowerCase().includes('sandbox'));
    };
    ensure(account.protocol === 'https:' && account.hostname === '[::1]' && account.port && account.pathname === '/' && !account.search && !account.hash);
    ensure(checkout.protocol === 'http:' && checkout.hostname === '127.0.0.1' && checkout.port && checkout.pathname === '/' && !checkout.search && !checkout.hash);
    ensure(/^[A-Za-z0-9+/]{43}=$/.test(pin));
    stage = 'tooling';
    const { chromium } = createRequire(import.meta.url)('/Users/mb5/projects/possums/node_modules/playwright-core');
    browser = await chromium.launch({executablePath:'/Users/mb5/Library/Caches/ms-playwright/chromium-1228/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing',headless:true,args:['--ignore-certificate-errors-spki-list='+pin,'--disable-background-networking','--no-proxy-server'],env:{PATH:process.env.PATH || '/usr/bin:/bin'}});
    const context = await browser.newContext({javaScriptEnabled:false});
    let external = false;
    await context.route('**/*', route => {
        try {
            const origin = new URL(route.request().url()).origin;
            if (origin === account.origin || origin === checkout.origin) return route.continue();
        } catch { /* invalid destination refused */ }
        external = true;
        return route.abort();
    });
    const page = await context.newPage();
    page.setDefaultTimeout(10000);
    stage = 'login';
    await page.goto(account.origin+'/login');
    stage = 'login_refusal';
    await page.locator('input[name="key"]').fill('D'.repeat(128));
    const [refused] = await Promise.all([
        page.waitForResponse(response => response.url() === account.origin+'/login' && response.request().method() === 'POST'),
        page.locator('form[action="/login"] button').click(),
    ]);
    ensure(refused.status() === 401);
    stage = 'login_notice';
    const error = page.locator('#form-error');
    ensure(await error.innerText() === 'Invalid credentials. Please try again.');
    ensure(await error.getAttribute('role') === 'alert');
    ensure(await page.locator('form[action="/login"]').getAttribute('aria-describedby') === 'form-error');
    ensure(await error.evaluate(element => getComputedStyle(element).color) === 'rgb(185, 28, 28)');
    const errorBox = await error.boundingBox();
    const buttonBox = await page.locator('form[action="/login"] button').boundingBox();
    ensure(errorBox && buttonBox && errorBox.y >= buttonBox.y + buttonBox.height);
    const refusedText = await page.locator('main').innerText();
    for (const value of ['SITE_AUTH_REFUSED', 'stage=', 'constraint=', 'outcome=', 'D'.repeat(128)]) ensure(!refusedText.includes(value));
    ensure(await page.locator('input[name="key"]').inputValue() === '');
    stage = 'login_confirm';
    await page.locator('input[name="key"]').fill('A'.repeat(128));
    await page.locator('form[action="/login"] button').click();
    await page.waitForURL(account.origin+'/home');
    stage = 'account';
    const homeText = await page.locator('main').innerText();
    ensure(homeText.includes('Balance: $5.00 USD') && homeText.includes('Available: $5.00 USD'));
    ensure(await page.locator('form[action="/topup/start"]').count() === 0);
    ensure(homeText.split('Balance: ').length === 2 && homeText.split('Available: ').length === 2);
    ensure(homeText.includes('Pending holds:\n(none)'));
    for (const field of ['synthetic-site-a','SYNTHETIC_A','Enabled','microunits','5000000','Account details','account_id=']) ensure(!homeText.includes(field));
    ensure(await page.locator('details').count() === 0);
    stage = 'form';
    await page.locator('a[href="/topup"]').click();
    await page.waitForURL(account.origin+'/topup');
    ensure(await page.locator('main h1').innerText() === 'Add credit');
    checkNotice(await page.locator('main').innerText());
    ensure(await page.locator('link[rel="stylesheet"][href^="/_site/styles/"]').count() === 1);
    ensure(await page.locator('main').evaluate(element => {
        const style = getComputedStyle(element);
        return style.fontFamily.includes('Iosevka') && style.whiteSpace === 'pre-wrap';
    }));
    ensure(await page.locator('script').count() === 0);
    ensure(await page.locator('a[href="/topup"]').count() === 0);
    ensure(await page.locator('a[href="/topup/status"]').count() === 0);
    ensure(await page.getByRole('link', {name:'Account',exact:true}).count() === 1);
    ensure(await page.locator('form[action="/topup/start"] button').allTextContents().then(labels => labels.join('|') === '$5.00|$10.00|$25.00|$50.00'));
    stage = 'checkout_navigation';
    await page.locator('button[value="usd10"]').click();
    await page.waitForURL(checkout.origin+'/checkout');
    stage = 'return';
    await page.locator('a').click();
    await page.waitForURL(account.origin+'/topup/return');
    const returnText = await page.locator('body').innerText();
    checkNotice(returnText);
    ensure(returnText.includes('payment and account funding are not confirmed'));
    ensure(returnText.includes('Current balance: $5.00 USD') && returnText.includes('Available: $5.00 USD'));
    for (const field of ['SYNTHETIC_A','Enabled','microunits','5000000','Pending holds:','Reservation:','Request key:']) ensure(!returnText.includes(field));
    ensure(await page.locator('main h1').innerText() === 'Add credit');
    ensure(await page.locator('form[action="/topup/start"]').count() === 0);
    ensure(await page.getByRole('link', {name:'Check payment status',exact:true}).getAttribute('href') === '/topup/status');
    ensure(await page.locator('a[href="/topup"]').count() === 0);
    ensure(await page.getByRole('link', {name:'Account',exact:true}).count() === 1);
    stage = 'privacy';
    ensure(!external);
    await context.close();
    passed = true;
} catch { /* only closed stage leaves this process */ }
finally {
    try { if (browser) await browser.close(); }
    catch { passed = false; stage = 'cleanup'; }
}
process.stdout.write(passed ? 'TOPUP_BROWSER_OK\n' : 'TOPUP_BROWSER_FAIL '+stage+'\n');
if (!passed) process.exitCode = 1;
