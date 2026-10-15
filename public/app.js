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
    let text;
    try {
      text = await response.text();
    } catch (e) {
      throw new Error('读取响应失败：' + e.message);
    }
    let data;
    try {
      data = JSON.parse(text);
    } catch (e) {
      // 兼容服务端偶发的多余字符：从第一个 '{' 起抢救第一个完整 JSON 对象。
      const start = text.indexOf('{');
      if (start > 0) {
        try { data = JSON.parse(text.slice(start)); } catch (_) { /* ignore */ }
      }
      if (data === undefined) {
        throw new Error('服务器返回的不是 JSON（HTTP ' + response.status + '）：' + text.slice(0, 200));
      }
    }
    return data;
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

// 专家写源：把逐字段实测报告（✓/✗ 每字段一行）展示在规则卡片下方。
function showExpertLog(data) {
  const box = $('expertOut');
  if (!box) return;
  if (data.version !== 'expert' || !data.expertLog) {
    box.classList.add('hidden');
    box.textContent = '';
    return;
  }
  box.classList.remove('hidden');
  const lines = data.expertLog.split('\n').filter((line) => line.trim());
  box.innerHTML = '<div class="name">专家写源 · 逐字段实测报告</div>' +
    lines.map((line) => {
      const bad = line.startsWith('✗');
      return `<div class="detail ${bad ? 'warn-text' : ''}" style="color:${bad ? 'var(--bad)' : 'var(--ok)'}">${escapeHTML(line)}</div>`;
    }).join('');
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
    const versionTag = data.version === 'full' ? '完整版' : (data.version === 'expert' ? '专家版' : '简写版');
    let done = isFix ? `已修复规则（${versionTag}，请重新验证）` : `已生成${versionTag}规则`;
    if (data.templateHit) done += ` — 命中模板：${data.templateHit}`;
    if (data.version === 'expert') {
      done += data.expertClean
        ? ` — 逐字段实测全部通过（回喂 ${data.expertRounds || 0} 轮）`
        : ` — 仍有字段未通过实测（回喂 ${data.expertRounds || 0} 轮，已输出最优版）`;
    }
    $('genStatus').textContent = done;
    showExpertLog(data);
    state.problems = [];
    $('btnFix').disabled = true;
    // 若已载入逐字段表格，把结果回填表格便于逐字段核对。
    if (fstate.dict.length > 0) {
      try {
        const parsed = JSON.parse($('rule').value);
        Object.entries(parsed).forEach(([k, v]) => { fstate.fill[k] = typeof v === 'string' ? v : JSON.stringify(v); });
        refreshFillInputs();
      } catch (_) { /* ignore */ }
    }
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

// ---- 逐字段写源表格 ----
// state.dict：字段字典（按分组）；state.fill：{字段名: 值}；state.finding：{字段名: 验证结论}
// state.templateHit / state.templateDefault：命中模板家族名与默认字段（简写版临时模板底座）

const fstate = { dict: [], fill: {}, finding: {}, templateHit: '', templateDefault: {} };

// 当前已填字段拼成规则 JSON 文本（主页url 允许缺，验证时站点地址兜底）。
function buildRuleFromFill() {
  const fields = {};
  Object.entries(fstate.fill).forEach(([name, value]) => {
    const v = String(value == null ? '' : value).trim();
    if (v !== '') fields[name] = v;
  });
  // 简写版临时模板：override 里与默认不同（或删除）的字段并入显式写出。
  const override = readTplOverride();
  Object.entries(override).forEach(([name, value]) => {
    const v = String(value == null ? '' : value).trim();
    if (v !== '' && fstate.templateDefault[name] !== v) fields[name] = v;
  });
  return JSON.stringify(fields, null, 2);
}

function readTplOverride() {
  const box = $('tplOverride');
  if (!box || !box.value.trim()) return {};
  try {
    const parsed = JSON.parse(box.value);
    const out = {};
    Object.entries(parsed).forEach(([k, v]) => { out[k] = typeof v === 'string' ? v : JSON.stringify(v); });
    return out;
  } catch (_) { return {}; }
}

async function loadDict() {
  const status = $('fieldStatus');
  status.textContent = '载入字段字典…';
  const category = fstate.fill['分类url'] || fstate.fill['分类Url'] || '';
  let data;
  try {
    data = await post('/api/fields', { categoryUrl: category }, 30000);
  } catch (e) {
    status.textContent = '载入失败：' + e.message;
    return;
  }
  if (!data.ok) { status.textContent = '载入失败：' + (data.error || '未知'); return; }
  fstate.dict = data.groups || [];
  fstate.templateHit = data.templateHit || '';
  try { fstate.templateDefault = data.templateJSON ? JSON.parse(data.templateJSON) : {}; }
  catch (_) { fstate.templateDefault = {}; }
  renderFieldTable();
  status.textContent = '字段表格就绪（共 ' + fstate.dict.reduce((n, g) => n + g.fields.length, 0) + ' 个字段）';
  if (fstate.templateHit) {
    status.textContent += ' · 命中模板：' + fstate.templateHit;
  }
}

function renderFieldTable() {
  const wrap = $('fieldTable');
  wrap.classList.remove('hidden');
  wrap.innerHTML = '';
  fstate.dict.forEach((group) => {
    const head = document.createElement('div');
    head.className = 'fgroup';
    head.textContent = group.name;
    wrap.appendChild(head);
    const table = document.createElement('table');
    table.className = 'frow-table';
    table.innerHTML = '<thead><tr><th class="c-name">字段</th><th class="c-val">值（填空）</th>'
      + '<th class="c-act">操作</th><th class="c-st">状态</th></tr></thead>';
    const tbody = document.createElement('tbody');
    group.fields.forEach((spec) => tbody.appendChild(buildFieldRow(spec)));
    table.appendChild(tbody);
    wrap.appendChild(table);
  });
  // 简写版临时模板编辑区
  const tplWrap = $('tplWrap');
  if (fstate.templateHit) {
    tplWrap.classList.remove('hidden');
    $('tplHit').textContent = '命中模板：' + fstate.templateHit;
    const box = $('tplOverride');
    if (!box.value.trim()) {
      box.value = JSON.stringify(fstate.templateDefault, null, 2);
    }
  } else {
    tplWrap.classList.add('hidden');
  }
}

function buildFieldRow(spec) {
  const tr = document.createElement('tr');
  tr.id = 'frow-' + spec.name;
  const omitTag = spec.omit ? '<span class="ftag ftag-omit">模板可省</span>' : '';
  const switchTag = spec.switch ? '<span class="ftag">开关</span>' : '';
  const nameCell = `<td class="c-name"><code>${escapeHTML(spec.name)}</code>${omitTag}${switchTag}
      <div class="fdesc">${escapeHTML(spec.desc || '')}</div>
      ${(spec.aliases && spec.aliases.length) ? `<div class="falias">别名：${escapeHTML(spec.aliases.join(' / '))}</div>` : ''}</td>`;
  const valCell = `<td class="c-val"><textarea class="fval" data-field="${escapeHTML(spec.name)}" rows="2"
      placeholder="填写后点「验证」">${escapeHTML(fstate.fill[spec.name] || '')}</textarea></td>`;
  const actCell = `<td class="c-act">
      <button class="mini btn-check" data-field="${escapeHTML(spec.name)}">验证</button>
      <button class="mini btn-fix" data-field="${escapeHTML(spec.name)}">AI纠正</button></td>`;
  const stCell = `<td class="c-st" id="fst-${escapeHTML(spec.name)}"></td>`;
  tr.innerHTML = nameCell + valCell + actCell + stCell;
  // 值输入时记录到 fill 并清旧状态
  const ta = tr.querySelector('.fval');
  ta.addEventListener('input', () => {
    fstate.fill[spec.name] = ta.value;
    setFieldStatus(spec.name, { status: '', message: '', evidence: '' });
  });
  tr.querySelector('.btn-check').addEventListener('click', () => verifyOneField(spec.name));
  tr.querySelector('.btn-fix').addEventListener('click', () => aiFixOneField(spec.name));
  return tr;
}

function setFieldStatus(name, finding) {
  const cell = $('fst-' + name);
  const row = $('frow-' + name);
  if (!cell || !row) return;
  fstate.finding[name] = finding;
  row.classList.remove('f-ok', 'f-bad', 'f-warn', 'f-skip');
  cell.className = 'c-st';
  cell.innerHTML = '';
  if (!finding || !finding.status) return;
  const map = { pass: 'f-ok', fail: 'f-bad', warn: 'f-warn', skip: 'f-skip' };
  row.classList.add(map[finding.status] || '');
  const label = { pass: '✓ 通过', fail: '✗ 失败', warn: '⚠ 存疑', skip: '— 跳过' }[finding.status] || finding.status;
  cell.innerHTML = `<span class="st-label">${label}</span>`
    + (finding.message ? `<div class="st-msg">${escapeHTML(finding.message)}</div>` : '')
    + (finding.evidence ? `<div class="st-ev">${escapeHTML(finding.evidence)}</div>` : '');
}

async function verifyOneField(name) {
  const cell = $('fst-' + name);
  if (cell) cell.innerHTML = '<span class="st-label">验证中…</span>';
  const rule = buildRuleFromFill();
  let data;
  try {
    data = await post('/api/check-field', {
      rule, field: name, site: $('site').value.trim(),
      samples: state.samples || [], keyword: $('keyword').value.trim() || '爱',
    }, 130000);
  } catch (e) {
    setFieldStatus(name, { status: 'fail', message: '验证请求失败：' + e.message, evidence: '' });
    return;
  }
  const finding = (data.findings || [])[0] || { status: 'fail', message: '无验证结论', evidence: '' };
  setFieldStatus(name, finding);
}

async function aiFixOneField(name) {
  const cell = $('fst-' + name);
  if (cell) cell.innerHTML = '<span class="st-label">AI 纠正中…</span>';
  const finding = fstate.finding[name] || {};
  let data;
  try {
    data = await post('/api/ai-fix-field', {
      site: $('site').value.trim(),
      field: name,
      value: fstate.fill[name] || '',
      problem: finding.message || '',
      samples: state.samples || [],
      notes: $('notes').value.trim(),
      rule: buildRuleFromFill(),
      baseUrl: $('baseUrl').value.trim(),
      apiKey: $('apiKey').value.trim(),
      model: $('model').value.trim(),
    }, 200000);
  } catch (e) {
    setFieldStatus(name, { status: 'fail', message: 'AI 纠正请求失败：' + e.message, evidence: '' });
    return;
  }
  if (!data.ok) {
    setFieldStatus(name, { status: 'warn', message: 'AI 纠正失败：' + (data.error || '未知'), evidence: '' });
    return;
  }
  fstate.fill[name] = data.value;
  const ta = document.querySelector(`.fval[data-field="${CSS.escape(name)}"]`);
  if (ta) ta.value = data.value;
  setFieldStatus(name, { status: 'pass', message: 'AI 已核实并更新该字段（' + (data.note || '') + '），正在回验…', evidence: '' });
  // 立即用服务端同一套实测回验一次，让行状态与真实结果一致（不会再无故标红）。
  await verifyOneField(name);
}

// 整表验证：串行验所有已填字段，失败字段标红。
async function validateAllFields() {
  const status = $('fieldStatus');
  const filled = Object.keys(fstate.fill).filter((k) => String(fstate.fill[k]).trim() !== '');
  if (filled.length === 0) { status.textContent = '表格还没有填写任何字段'; return; }
  const rule = buildRuleFromFill();
  status.textContent = `整表验证中…（${filled.length} 个字段）`;
  let data;
  try {
    data = await post('/api/check-field', {
      rule, field: '', site: $('site').value.trim(),
      samples: state.samples || [], keyword: $('keyword').value.trim() || '爱',
    }, 290000);
  } catch (e) {
    status.textContent = '整表验证失败：' + e.message;
    return;
  }
  const findings = data.findings || [];
  let failCount = 0;
  findings.forEach((f) => {
    setFieldStatus(f.field, f);
    if (f.status === 'fail') failCount++;
  });
  status.textContent = failCount === 0
    ? `整表验证通过（${findings.length} 字段，无失败）`
    : `整表验证完成：${failCount} 个字段失败（已标红，点各行「AI纠正」修复）`;
  return findings;
}

// 字段名归一：把别名（分类Url/图片/搜索Url…）归到字典规范名，避免重复行。
function canonicalFieldName(name) {
  for (const group of fstate.dict) {
    for (const spec of group.fields) {
      if (spec.name === name) return spec.name;
      if (spec.aliases && spec.aliases.includes(name)) return spec.name;
    }
  }
  return name;
}

// 按字段生成源 JSON：整表验证通过后，交给后端按字典顺序统一生成。
async function generateJSONFromFields() {
  const status = $('fieldStatus');
  status.textContent = '整表验证并生成 JSON…';
  const findings = await validateAllFields();
  if (!findings) return;
  const bad = findings.filter((f) => f.status === 'fail');
  // 容错机制：有标红字段时不再直接拒绝，而是弹出确认——
  // 用户确认「是」则强制生成并写入规则框（标红字段仍按当前填写值写入），
  // 选择「否」则保持原规则框内容不变。
  if (bad.length > 0) {
    const names = bad.map((f) => f.field || f.Field).filter(Boolean);
    const listText = names.length > 0 ? `\n标红字段：${names.join('、')}` : '';
    const forced = window.confirm(
      `⚠️ 仍有 ${bad.length} 个字段验证未通过（标红）。${listText}\n\n` +
      '是否强制生成源 JSON 并填入规则框？\n' +
      '· 点「确定」= 强制生成（标红字段按当前填写值写入，可能需要后续修正）\n' +
      '· 点「取消」= 不生成，保留规则框现有内容'
    );
    if (!forced) {
      status.textContent = `已取消：仍有 ${bad.length} 个标红字段，未生成（规则框保持原内容）`;
      return;
    }
  }
  let data;
  try {
    data = await post('/api/gen-json', { rule: buildRuleFromFill() }, 30000);
  } catch (e) {
    status.textContent = '生成 JSON 失败：' + e.message;
    return;
  }
  if (!data.ok) { status.textContent = '生成失败：' + (data.error || '未知'); return; }
  $('rule').value = data.json;
  try {
    const parsed = JSON.parse(data.json);
    Object.entries(parsed).forEach(([k, v]) => { fstate.fill[k] = typeof v === 'string' ? v : JSON.stringify(v); });
  } catch (_) { /* ignore */ }
  status.textContent = bad.length > 0
    ? `已强制生成源 JSON 并写入规则框（⚠️ 含 ${bad.length} 个标红字段，建议后续逐个修正）`
    : '已按字段字典顺序生成源 JSON 并写入规则框';
}

// 把规则 JSON 载入表格：解析 #rule 文本填进 fill（别名归一）。
function syncRuleToFill() {
  const text = $('rule').value.trim();
  if (!text) { $('fieldStatus').textContent = '规则 JSON 为空，无可载入内容'; return; }
  let parsed;
  try { parsed = JSON.parse(text); }
  catch (e) { $('fieldStatus').textContent = '规则 JSON 解析失败：' + e.message; return; }
  fstate.fill = {};
  Object.entries(parsed).forEach(([k, v]) => {
    const name = canonicalFieldName(k);
    fstate.fill[name] = typeof v === 'string' ? v : JSON.stringify(v);
  });
  if (fstate.dict.length === 0) {
    loadDict().then(() => refreshFillInputs());
  } else {
    renderFieldTable();
    refreshFillInputs();
  }
  $('fieldStatus').textContent = '已从规则 JSON 载入 ' + Object.keys(fstate.fill).length + ' 个字段到表格';
}

function refreshFillInputs() {
  Object.entries(fstate.fill).forEach(([name, value]) => {
    const ta = document.querySelector(`.fval[data-field="${CSS.escape(name)}"]`);
    if (ta) ta.value = value;
  });
}

// ---- 绑定 ----

$('btnProbe').addEventListener('click', probe);
$('btnGenerate').addEventListener('click', () => generate(false));
$('btnFix').addEventListener('click', () => generate(true));
$('btnVerify').addEventListener('click', verify);
$('btnCopy').addEventListener('click', copyRule);
$('btnDownload').addEventListener('click', downloadRule);
$('btnLoadDict').addEventListener('click', loadDict);
$('btnSyncRule').addEventListener('click', syncRuleToFill);
$('btnValidateAll').addEventListener('click', validateAllFields);
$('btnGenJSON').addEventListener('click', generateJSONFromFields);
// 临时模板编辑：内容变化即重渲染（不改模板固定格式，只影响生成的 JSON）。
if ($('tplOverride')) $('tplOverride').addEventListener('change', () => {
  fstate.templateDefault = fstate.templateDefault || {};
});
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
