// wrxbpq 前端控制逻辑。
// 验证拆成 4 批串行请求，避免单次请求时间过长被平台超时掐断。

const BATCHES = [
  ['parse', 'catalog'],
  ['paging', 'search'],
  ['detail'],
  ['play'],
];

const STEP_NAMES = {
  parse: '规则解析',
  catalog: '目录抓取',
  paging: '分页翻页',
  search: '搜索',
  detail: '详情与分集',
  play: '播放解析',
};

const $ = (id) => document.getElementById(id);
const state = { problems: [], detailId: '', running: false, samples: [], fingerprint: '' };

// ---- 配置持久化 ----

function loadConfig() {
  try {
    const saved = JSON.parse(localStorage.getItem('wrxbpq.ai') || '{}');
    if (saved.baseUrl) $('baseUrl').value = saved.baseUrl;
    if (saved.model) $('model').value = saved.model;
    if (saved.apiKey) $('apiKey').value = saved.apiKey;
    if (saved.notes) $('notes').value = saved.notes;
    if (saved.version && $('ruleVersion')) $('ruleVersion').value = saved.version;
  } catch (_) { /* 忽略损坏的本地配置 */ }
}

function saveConfig() {
  localStorage.setItem('wrxbpq.ai', JSON.stringify({
    baseUrl: $('baseUrl').value.trim(),
    model: $('model').value.trim(),
    apiKey: $('apiKey').value.trim(),
    notes: $('notes').value.trim(),
    version: $('ruleVersion') ? $('ruleVersion').value : 'simple',
  }));
}

async function post(path, payload, timeoutMs) {
  const controller = new AbortController();
  const timer = timeoutMs ? setTimeout(() => controller.abort(), timeoutMs) : null;
  try {
    const response = await fetch(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
      signal: controller.signal,
    });
    return await response.json();
  } catch (error) {
    if (error.name === 'AbortError') {
      throw new Error('请求超时（超过 ' + Math.round(timeoutMs / 1000) + ' 秒未响应），可能是模型太慢或 Vercel 函数超时。建议本地运行生成，或换更快的模型。');
    }
    throw error;
  } finally {
    if (timer) clearTimeout(timer);
  }
}

// ---- 步骤渲染 ----

function renderSteps() {
  const box = $('steps');
  box.innerHTML = '';
  BATCHES.flat().forEach((step) => {
    const div = document.createElement('div');
    div.className = 'step pending';
    div.id = 'step-' + step;
    div.innerHTML = `<div class="name">${STEP_NAMES[step] || step}</div>
      <div class="msg">待执行</div>`;
    box.appendChild(div);
  });
}

function paintStep(result) {
  const box = $('step-' + result.step);
  if (!box) return;
  // 三种状态：失败 bad / 通过但有警告 warn（如 CDN 地域封锁）/ 通过 ok
  const cls = !result.ok ? 'bad' : (result.warning ? 'warn' : 'ok');
  box.className = 'step ' + cls;
  box.innerHTML = `<div class="name">${STEP_NAMES[result.step] || result.step}</div>
    <div class="msg">${escapeHTML(result.message || '')}</div>`;
  if (result.warning) {
    box.insertAdjacentHTML('beforeend',
      `<div class="detail warn-text">⚠ ${escapeHTML(result.warning)}</div>`);
  }
  if (result.error) {
    box.querySelector('.msg').insertAdjacentHTML('afterend',
      `<div class="detail">错误：${escapeHTML(result.error)}</div>`);
  }
  if (result.details) {
    const lines = Object.entries(result.details)
      .map(([key, value]) => `${key} = ${typeof value === 'string' ? value : JSON.stringify(value)}`)
      .join('\n');
    box.insertAdjacentHTML('beforeend', `<div class="detail">${escapeHTML(lines)}</div>`);
  }
}

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, (char) => (
    { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[char]
  ));
}

// ---- 动作 ----

async function probe() {
  const site = $('site').value.trim();
  if (!site) return alert('请先填写站点地址');
  const out = $('probeOut');
  out.classList.remove('hidden');
  out.textContent = '抓取中…';
  try {
    const data = await post('/api/probe', { site }, 90000);
    if (!data.ok) {
      out.textContent = '抓取失败：' + (data.error || '未知错误');
      return;
    }
    state.samples = data.samples || [];
    state.fingerprint = data.fingerprint || '';
    const summary = (data.samples || [])
      .map((s) => `${s.label} — ${s.content.length} 字符`)
      .join('\n');
    out.textContent = summary
      + (data.fingerprint ? '\n\n' + data.fingerprint : '')
      + '\n\n✅ 基础数据已就绪，可以点「AI 生成规则」。';
    updateGenerateEnabled();
  } catch (error) {
    out.textContent = '抓取失败：' + error.message;
  }
}

// 生成耗时反馈：每秒刷新已等待时间，避免看起来像卡死。
let elapsedTimer = null;
function startElapsed(label) {
  const started = Date.now();
  stopElapsed();
  elapsedTimer = setInterval(() => {
    const secs = Math.round((Date.now() - started) / 1000);
    $('genStatus').textContent = `${label}（已等待 ${secs} 秒）`;
  }, 1000);
}
function stopElapsed() {
  if (elapsedTimer) { clearInterval(elapsedTimer); elapsedTimer = null; }
}

async function generate(isFix) {
  const site = $('site').value.trim();
  if (!site) return alert('请先填写站点地址');
  const rule = $('rule').value.trim();
  if (isFix && !rule) return alert('没有可修复的规则');
  if (isFix && state.problems.length === 0) {
    return alert('没有验证失败信息可交给 AI——请先点「开始验证」，验证出现失败后修复按钮才有内容。');
  }
  if (!isFix && (!state.samples || state.samples.length === 0)) {
    return alert('必须先点击「抓取样本」获取站点基础数据，AI 才能依据真实页面写规则。\n现在就去抓取？');
  }
  const label = isFix ? 'AI 修复中…' : 'AI 生成中…';
  $('genStatus').textContent = label;
  startElapsed(label);
  setBusy(true);
  try {
    const payload = {
      site,
      rule: isFix ? rule : '',
      problems: isFix ? state.problems : [],
      notes: $('notes').value.trim(),
      version: $('ruleVersion') ? $('ruleVersion').value : 'simple',
      baseUrl: $('baseUrl').value.trim(),
      apiKey: $('apiKey').value.trim(),
      model: $('model').value.trim(),
    };
    // 带上缓存样本，修复时不必让服务器重新抓取站点。
    if (state.samples && state.samples.length) payload.samples = state.samples;
    // 290 秒 < Vercel 函数 300 秒上限，抢在平台掐断前拿到明确错误。
    const data = await post('/api/generate', payload, 290000);
    stopElapsed();
    if (!data.ok) {
      $('genStatus').textContent = '失败：' + (data.error || '未知错误');
      if (data.raw) console.log(data.raw);
      return;
    }
    $('rule').value = data.rule;
    if (data.samples && data.samples.length) state.samples = data.samples;
    if (data.fingerprint) state.fingerprint = data.fingerprint;
    const versionTag = data.version === 'full' ? '完整版' : '简写版';
    let done = isFix ? `已修复规则（${versionTag}，请重新验证）` : `已生成${versionTag}规则`;
    if (data.templateHit) done += ` — 命中模板：${data.templateHit}`;
    $('genStatus').textContent = done;
    state.problems = [];
    $('btnFix').disabled = true;
  } catch (error) {
    stopElapsed();
    $('genStatus').textContent = '失败：' + error.message;
  } finally {
    stopElapsed();
    setBusy(false);
  }
}

async function verify() {
  const rule = $('rule').value.trim();
  if (!rule) return alert('请先填写或生成规则 JSON');
  renderSteps();
  state.problems = [];
  state.detailId = '';
  setBusy(true);
  try {
    for (const batch of BATCHES) {
      const payload = {
        rule,
        site: $('site').value.trim(),
        keyword: $('keyword').value.trim() || '爱',
        steps: batch,
      };
      if (state.detailId) payload.detailId = state.detailId;
      let data;
      try {
        data = await post('/api/verify', payload, 290000);
      } catch (error) {
        batch.forEach((step) => {
          const box = $('step-' + step);
          if (box && box.className.indexOf('ok') < 0 && box.className.indexOf('bad') < 0) {
            box.className = 'step bad';
            box.querySelector('.msg').textContent = error.message;
            state.problems.push(`[${step}] ${error.message}`);
          }
        });
        break;
      }
      (data.results || []).forEach((result) => {
        paintStep(result);
        if (!result.ok && result.error) state.problems.push(`[${result.step}] ${result.error}`);
        if (result.seed && result.seed.detailId) state.detailId = result.seed.detailId;
      });
      const failed = (data.results || []).some((r) => !r.ok);
      if (failed) break;
    }
  } catch (error) {
    $('genStatus').textContent = '验证请求失败：' + error.message;
  } finally {
    setBusy(false);
    $('btnFix').disabled = state.problems.length === 0;
  }
}

function setBusy(busy) {
  state.running = busy;
  ['btnProbe', 'btnGenerate', 'btnVerify', 'btnFix'].forEach((id) => {
    $(id).disabled = busy;
  });
  const sel = $('ruleVersion');
  if (sel) sel.disabled = busy;
  if (!busy) updateGenerateEnabled();
}

// 强制依赖抓取：没有样本就不允许点「AI 生成规则」。
function updateGenerateEnabled() {
  const btn = $('btnGenerate');
  if (!btn) return;
  const ready = state.samples && state.samples.length > 0;
  btn.disabled = state.running || !ready;
  btn.title = ready ? '' : '请先点击「抓取样本」获取站点基础数据';
}

function copyRule() {
  const rule = $('rule').value;
  if (!rule) return;
  navigator.clipboard.writeText(rule).then(() => {
    $('genStatus').textContent = '已复制到剪贴板';
  });
}

function downloadRule() {
  const rule = $('rule').value;
  if (!rule) return;
  let name = 'rule';
  try {
    const parsed = JSON.parse(rule);
    const home = parsed['主页url'] || parsed['首页url'] || '';
    const host = (home.match(/https?:\/\/([^/]+)/) || [])[1];
    if (host) name = host;
  } catch (_) { /* JSON 不合法时用默认名 */ }
  const blob = new Blob([rule], { type: 'application/json' });
  const link = document.createElement('a');
  link.href = URL.createObjectURL(blob);
  link.download = name + '.json';
  link.click();
  URL.revokeObjectURL(link.href);
}

// ---- 绑定 ----

$('btnProbe').addEventListener('click', probe);
$('btnGenerate').addEventListener('click', () => generate(false));
$('btnFix').addEventListener('click', () => generate(true));
$('btnVerify').addEventListener('click', verify);
$('btnCopy').addEventListener('click', copyRule);
$('btnDownload').addEventListener('click', downloadRule);
['baseUrl', 'model', 'apiKey', 'notes'].forEach((id) => {
  $(id).addEventListener('change', saveConfig);
});
// 版本选择同样持久化，刷新页面后保持上次选的简写/完整版。
if ($('ruleVersion')) $('ruleVersion').addEventListener('change', saveConfig);
// 站点地址一变，旧样本即失效，必须重新抓取才能生成。
$('site').addEventListener('input', () => {
  if (state.samples && state.samples.length) {
    state.samples = [];
    state.fingerprint = '';
    const out = $('probeOut');
    if (!out.classList.contains('hidden')) {
      out.textContent = '站点地址已更改，请重新「抓取样本」。';
    }
    updateGenerateEnabled();
  }
});

loadConfig();
renderSteps();
updateGenerateEnabled();
