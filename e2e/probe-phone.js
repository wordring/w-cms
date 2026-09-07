// スマホ幅で「はみ出し」と「薄青・薄赤の見分け」を測る（2026-09-07）。
//
// 引き継ぎに残っていた宿題2つ:
//   ① リンクのプロパティ欄が画面右へはみ出さないか（.col-popover は端へのクランプ無し）
//   ② 薄青・薄赤の見分けが小画面でつくか
//
// **測り方の注意**（1度間違えたので書き残す）:
//   - ポップオーバは閲覧モードだと祖先ごと隠れていて**寸法が0**になる。
//     写しを body へ置いて実寸を測る。
//   - 2色の見分けに**コントラスト比を使ってはいけない**。あれは文字と地の明度比で、
//     同じ明るさの青と赤は 1.00:1 になる——見分けは付くのに。
//     色の差は **CIE Lab の ΔE**、色覚の型は**シミュレーション**で見る。
const { chromium } = require('playwright');

const BASE = process.env.WCMS_BASE || 'http://localhost:8080';
const USER = process.env.WCMS_USER || 'a';
const PASS = process.env.WCMS_PASS || 'a';
const WIDTHS = [320, 390];

function parseRGB(s) {
  const m = /rgba?\((\d+),\s*(\d+),\s*(\d+)/.exec(s || '');
  return m ? [+m[1], +m[2], +m[3]] : null;
}

// sRGB → Lab（D65）。ΔE を出すため。
function lab(rgb) {
  const f = (c) => {
    const s = c / 255;
    return s <= 0.04045 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  };
  const [r, g, b] = rgb.map(f);
  const X = (0.4124 * r + 0.3576 * g + 0.1805 * b) / 0.95047;
  const Y = 0.2126 * r + 0.7152 * g + 0.0722 * b;
  const Z = (0.0193 * r + 0.1192 * g + 0.9505 * b) / 1.08883;
  const k = (t) => (t > 0.008856 ? Math.cbrt(t) : 7.787 * t + 16 / 116);
  const [fx, fy, fz] = [k(X), k(Y), k(Z)];
  return [116 * fy - 16, 500 * (fx - fy), 200 * (fy - fz)];
}

function deltaE(a, b) {
  const [l1, a1, b1] = lab(a), [l2, a2, b2] = lab(b);
  return Math.sqrt((l1 - l2) ** 2 + (a1 - a2) ** 2 + (b1 - b2) ** 2);
}

// 2型（緑）・1型（赤）色覚のごく素朴なシミュレーション（Brettel 系の線形近似）。
function simulate(rgb, kind) {
  const [r, g, b] = rgb;
  if (kind === 'deuteranopia') {
    return [0.625 * r + 0.7 * g + 0.0 * b, 0.7 * r + 0.3 * g + 0.0 * b, 0.0 * r + 0.3 * g + 0.7 * b]
      .map((v) => Math.max(0, Math.min(255, Math.round(v / 1.325 * 1.0))));
  }
  // protanopia
  return [0.567 * r + 0.433 * g, 0.558 * r + 0.442 * g, 0.242 * g + 0.758 * b]
    .map((v) => Math.max(0, Math.min(255, Math.round(v))));
}

(async () => {
  const browser = await chromium.launch();
  for (const width of WIDTHS) {
    const ctx = await browser.newContext({
      viewport: { width, height: 700 }, ignoreHTTPSErrors: true,
      hasTouch: true, isMobile: true,
    });
    const page = await ctx.newPage();
    await page.goto(BASE + '/login');
    await page.fill('#username', USER);
    await page.fill('#password', PASS);
    await page.click('button[type=submit]');
    await page.waitForLoadState('networkidle');

    console.log('\n=== 幅 ' + width + 'px ===');

    const overflow = await page.evaluate(() => ({
      doc: document.documentElement.scrollWidth, win: window.innerWidth,
    }));
    console.log('  本文の横はみ出し:',
      overflow.doc > overflow.win ? '⚠ あり（' + overflow.doc + ' > ' + overflow.win + '）' : 'なし');

    // ── ① ポップオーバの実寸を測り、右端で開いたときのはみ出しを出す
    const pop = await page.evaluate(() => {
      const src = document.getElementById('w-link-popover');
      if (!src) return { ok: false, why: '要素が無い' };
      // **写しを body へ置いて測る**——本体は閲覧モードだと祖先ごと隠れていて 0 になる。
      const clone = src.cloneNode(true);
      clone.classList.add('active');
      clone.style.position = 'absolute';
      clone.style.left = '0px';
      clone.style.top = '0px';
      clone.style.visibility = 'hidden';
      document.body.appendChild(clone);
      const w = Math.round(clone.getBoundingClientRect().width);
      clone.remove();
      return { ok: true, width: w, win: window.innerWidth };
    });
    if (!pop.ok) {
      console.log('  ポップオーバ:', pop.why);
    } else {
      // いまの実装は「押した要素の左端に合わせる」だけ。右寄りの要素で開くと…
      const anchorLeft = pop.win - 40; // 画面右端近くのリンクを押した場合
      const over = anchorLeft + pop.width - pop.win;
      console.log('  ポップオーバの実寸: ' + pop.width + 'px（画面 ' + pop.win + 'px）');
      console.log('  右端近くで開くと:',
        over > 0 ? '⚠ ' + over + 'px はみ出す' : '収まる');
    }

    // ── ② 薄青・薄赤の見分け（ΔE と 色覚シミュレーション）
    const colours = await page.evaluate(() => {
      const host = document.getElementById('w-editor-content');
      const dl = document.createElement('dl');
      dl.setAttribute('data-type', 'tags');
      dl.innerHTML = '<dt>名前</dt><dd>値</dd>';
      host.appendChild(dl);
      const blue = getComputedStyle(dl.querySelector('dt')).backgroundColor;
      dl.remove();
      const span = document.createElement('span');
      span.className = 'ref-missing';
      span.textContent = 'x';
      host.appendChild(span);
      const red = getComputedStyle(span).backgroundColor;
      span.remove();
      return { blue, red };
    });
    const blue = parseRGB(colours.blue), red = parseRGB(colours.red);
    if (blue && red) {
      console.log('  薄青 ' + colours.blue + ' / 薄赤 ' + colours.red);
      console.log('  色の差 ΔE = ' + deltaE(blue, red).toFixed(1) +
        '（10以上で「別の色」と分かる目安・2以下はほぼ同じ）');
      for (const kind of ['deuteranopia', 'protanopia']) {
        const d = deltaE(simulate(blue, kind), simulate(red, kind));
        console.log('  ' + (kind === 'deuteranopia' ? '2型（緑）' : '1型（赤）') +
          '色覚での差 ΔE = ' + d.toFixed(1));
      }
    }
    await ctx.close();
  }
  await browser.close();
})();
