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
const state = { problems: [], detailId: '', running: false };

// ---- 配置持久化 ----

function loadConfig() {
  try {
    const saved = JSON.parse(localStorage.getItem('wrxbpq.ai') || '{}');
    if (saved.baseUrl) $('baseUrl').value = saved.baseUrl;
    if (saved.model) $('model').value = saved.model;
    if (saved.apiKey) $('apiKey').value = saved.apiKey;
  } catch (_) { /* 忽略损坏的本地配置 */ }
}

function saveConfig() {
  localStorage.setItem('wrxbpq.ai', JSON.stringify({
    baseUrl: $('baseUrl').value.trim(),
    model: $('model').value.trim(),
    apiKey: $('apiKey').value.trim(),
  }));
}

async function post(path, payload) {
  const response = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
  return response.json();
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
  box.className = 'step ' + (result.ok ? 'ok' : 'bad');
  box.innerHTML = `<div class="name">${STEP_NAMES[result.step] || result.step}</div>
    <div class="msg">${escapeHTML(result.message || '')}</div>`;
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
  const data = await post('/api/probe', { site });
  if (!data.ok) {
    out.textContent = '抓取失败：' + (data.error || '未知错误');
    return;
  }
  out.textContent = (data.samples || [])
    .map((s) => `${s.label} — ${s.content.length} 字符`)
    .join('\n');
}

async function generate(isFix) {
  const site = $('site').value.trim();
  if (!site) return alert('请先填写站点地址');
  const rule = $('rule').value.trim();
  if (isFix && !rule) return alert('没有可修复的规则');
  $('genStatus').textContent = isFix ? 'AI 修复中…' : 'AI 生成中…';
  setBusy(true);
  try {
    const data = await post('/api/generate', {
      site,
      rule: isFix ? rule : '',
      problems: isFix ? state.problems : [],
      baseUrl: $('baseUrl').value.trim(),
      apiKey: $('apiKey').value.trim(),
      model: $('model').value.trim(),
    });
    if (!data.ok) {
      $('genStatus').textContent = '失败：' + (data.error || '未知错误');
      if (data.raw) console.log(data.raw);
      return;
    }
    $('rule').value = data.rule;
    $('genStatus').textContent = '已生成规则';
  } finally {
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
      const data = await post('/api/verify', payload);
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
['baseUrl', 'model', 'apiKey'].forEach((id) => {
  $(id).addEventListener('change', saveConfig);
});

loadConfig();
renderSteps();
