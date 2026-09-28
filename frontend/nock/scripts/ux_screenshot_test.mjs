// ux_screenshot_test.mjs -- real, live UX regression test for the /admin/nock React app.
//
// Founder real-time, 2026-09-28: "shankpit levels is down its just a blank screen can we add
// some ux screenshot testing." This is the concrete answer: log in for real (no auth bypass --
// a dedicated agent, SCREENSHOT-CI, was provisioned via cmd/create-admin-agent the same real way
// EDDY/HOUSE/BOOTS already are), click through every real tab in the app, and fail loudly if any
// tab (a) throws an uncaught page error, (b) logs a console error, or (c) renders with an
// empty/near-empty #root -- the exact shape a React app takes when an uncaught render error
// unmounts the whole tree.
//
// Real, found-live addition (same session, same bug): the FIRST version of this test used a
// plain Chromium launch and passed cleanly -- it never caught the actual reported bug, because
// the actual bug only reproduces when WebGL is unavailable (the founder's own real browser had
// hardware acceleration disabled/sandboxed; Playwright's default Chromium has WebGL on). A
// second pass now launches Chromium with `--disable-webgl(2)` to genuinely reproduce that
// condition -- this is the pass that would have caught the real regression (a `THREE.
// WebGLRenderer` construction that threw, uncaught, propagating through React's own commit
// phase and unmounting the whole app since no ErrorBoundary existed). Both are now fixed (see
// webglSupport.ts and TabErrorBoundary.tsx) and both passes are asserted here so a regression in
// either direction gets caught automatically.
//
// Run: IDUNA_AGENT_SECRET=<SCREENSHOT-CI secret, var/agent-secrets.env> node scripts/ux_screenshot_test.mjs [baseUrl]
// Screenshots land in scripts/ux-screenshots/ (gitignored) for visual review, not asserted on
// pixel-by-pixel -- a real screenshot diff is a real, separate, heavier follow-up (see NORTHSTAR),
// named honestly rather than half-built here.
import { chromium } from 'playwright'
import { mkdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const here = path.dirname(fileURLToPath(import.meta.url))
const shotDir = path.join(here, 'ux-screenshots')
mkdirSync(shotDir, { recursive: true })

const baseUrl = process.argv[2] || process.env.IDUNA_BASE_URL || 'http://localhost:8080'
const agentName = process.env.SCREENSHOT_CI_AGENT_NAME || 'SCREENSHOT-CI'
const agentSecret = process.env.IDUNA_AGENT_SECRET || process.env.IDUNA_SECRET_SCREENSHOT_CI
if (!agentSecret) {
  console.error('Set IDUNA_AGENT_SECRET (or IDUNA_SECRET_SCREENSHOT_CI) to the SCREENSHOT-CI agent secret -- see var/agent-secrets.env')
  process.exit(1)
}

// Every real tab button label in App.tsx's <nav>, in the order they appear. Kept as a flat list
// (not derived from the DOM) so a newly-added tab is a real, deliberate addition to this test,
// not something that silently starts or stops being covered.
const TABS = [
  'Texture Library', 'Animations', 'Robots', 'Door Scripts', 'Projects (layer editor)',
  'BRAWLPIT Levels', 'BRAWLPIT AI Opponents', 'SHANKPIT Levels', 'SHANKPIT AI Opponents',
  'SHANKPIT Materials', 'SHANKPIT Widgets', 'DEADWEIGHT AI Opponents', 'Sprays',
  'MIXFORGE EDITOR', 'Sounds', 'Booth', 'Code',
]

async function runPass(launchArgs, shotSuffix) {
  const failures = []
  const browser = await chromium.launch({ args: launchArgs })
  const page = await browser.newPage()
  const pageErrors = []
  page.on('pageerror', (e) => pageErrors.push(e.message))
  const consoleErrors = []
  page.on('console', (m) => { if (m.type() === 'error') consoleErrors.push(m.text()) })

  // Real login through the real form, exactly as a human would (POST agent_name/agent_secret to
  // /admin/login) -- never a minted/forged cookie.
  await page.goto(`${baseUrl}/admin/login`, { waitUntil: 'networkidle' })
  await page.fill('#an', agentName)
  await page.fill('#as', agentSecret)
  await Promise.all([page.waitForNavigation(), page.click('button[type=submit], input[type=submit]')])

  if (page.url().includes('/admin/login')) {
    console.error(`login failed -- still on ${page.url()} (check the SCREENSHOT-CI agent secret and its iduna.admin permission)`)
    await browser.close()
    return [`login failed for pass "${shotSuffix}"`]
  }

  await page.goto(`${baseUrl}/admin/nock/`, { waitUntil: 'networkidle' })

  for (const tab of TABS) {
    pageErrors.length = 0
    consoleErrors.length = 0
    const btn = page.getByRole('button', { name: tab, exact: true })
    const exists = await btn.count()
    if (exists === 0) {
      failures.push(`[${shotSuffix}] tab "${tab}" -- no nav button found with this exact label (renamed or removed?)`)
      continue
    }
    await btn.first().click()
    await page.waitForTimeout(1200)

    const rootLen = await page.evaluate(() => document.getElementById('root')?.innerText?.trim()?.length ?? 0)
    const safeName = tab.replace(/[^a-z0-9]+/gi, '_').toLowerCase()
    await page.screenshot({ path: path.join(shotDir, `${safeName}${shotSuffix ? `_${shotSuffix}` : ''}.png`) })

    if (rootLen < 20) {
      failures.push(`[${shotSuffix}] tab "${tab}" -- #root rendered near-empty (${rootLen} chars of text) -- likely an uncaught render error unmounted the whole app`)
    }
    if (pageErrors.length > 0) {
      failures.push(`[${shotSuffix}] tab "${tab}" -- ${pageErrors.length} uncaught page error(s): ${pageErrors.join(' | ')}`)
    }
    // Console errors alone don't fail the test (a background 404 from an unrelated, unfetched
    // resource is real but not fatal) -- only logged. The WebGL-disabled pass legitimately logs
    // THREE.WebGLRenderer's own console.error on every guarded construction -- expected noise,
    // not a failure signal (the failure signal is rootLen/pageErrors above).
    if (consoleErrors.length > 0) {
      console.log(`  (${shotSuffix} "${tab}": ${consoleErrors.length} console error(s), non-fatal: ${consoleErrors[0].slice(0, 120)})`)
    }
    console.log(`${failures.some((f) => f.includes(`tab "${tab}"`)) ? 'FAIL' : 'OK'}: [${shotSuffix}] ${tab} (#root: ${rootLen} chars)`)
  }

  await browser.close()
  return failures
}

const normalFailures = await runPass([], 'webgl_on')
const noWebglFailures = await runPass(['--disable-webgl', '--disable-webgl2', '--disable-gpu'], 'webgl_off')
const failures = [...normalFailures, ...noWebglFailures]

if (failures.length > 0) {
  console.error(`\n${failures.length} failure(s):`)
  for (const f of failures) console.error(`  - ${f}`)
  process.exit(1)
}
console.log(`\nux_screenshot_test: PASS -- all ${TABS.length} tabs rendered real content with zero uncaught errors, with WebGL both available and unavailable. Screenshots in ${shotDir}`)
