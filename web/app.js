(function () {
  'use strict';

  var STATE_TEXT = {
    idle: '空闲',
    running: '运行中',
    stopped: '已停止',
    done: '已完成',
    error: '出错'
  };

  var STAGE_TEXT = {
    hot: '抓取热榜',
    list: '抓取列表页',
    detail: '抓取详情页',
    '': '—'
  };

  var ERROR_TEXT = {
    no_keywords: '请填写关键词，或给出热榜条数',
    bad_limit: '条数上限需在 1–24 之间',
    bad_hot: '热榜条数需在 0–50 之间',
    job_running: '已有任务正在运行，请先停止',
    bad_request: '请求格式有误'
  };

  var els = {
    form: document.getElementById('submit-form'),
    keywords: document.getElementById('keywords-input'),
    hot: document.getElementById('hot-input'),
    limit: document.getElementById('limit-input'),
    detail: document.getElementById('detail-toggle'),
    submit: document.getElementById('job-submit'),
    stop: document.getElementById('job-stop'),
    formError: document.getElementById('form-error'),
    state: document.getElementById('progress-state'),
    stage: document.getElementById('progress-stage'),
    keyword: document.getElementById('progress-keyword'),
    done: document.getElementById('progress-done'),
    total: document.getElementById('progress-total'),
    delay: document.getElementById('progress-delay'),
    note: document.getElementById('progress-note'),
    summary: document.getElementById('result-summary'),
    body: document.getElementById('result-body'),
    empty: document.getElementById('result-empty'),
    copyAll: document.getElementById('copy-all'),
    exportJson: document.getElementById('export-json'),
    exportCsv: document.getElementById('export-csv')
  };

  var items = [];
  var jobId = '';
  var source = null;
  var running = false;

  function showError(message) {
    els.formError.textContent = message;
    els.formError.hidden = !message;
  }

  function setRunning(isRunning) {
    running = isRunning;
    els.submit.disabled = isRunning;
    els.stop.disabled = !isRunning || !jobId;
    els.submit.textContent = isRunning ? '抓取中…' : '开始抓取';
  }

  function setState(state) {
    var text = STATE_TEXT[state] || state || '—';
    els.state.textContent = text;
    els.state.setAttribute('data-state', state || 'idle');
  }

  function setDelay(seconds, reason) {
    var suffix = reason === 'retry' ? '（重试）' : '';
    els.delay.textContent = seconds.toFixed(2) + ' 秒' + suffix;
  }

  function setNote(note) {
    els.note.textContent = note || '';
    els.note.hidden = !note;
  }

  function updateSummary() {
    els.summary.textContent = '共 ' + items.length + ' 条';
    els.empty.hidden = items.length > 0;
    var disabled = items.length === 0;
    els.copyAll.disabled = disabled;
    els.exportJson.classList.toggle('cs-button--disabled', disabled);
    els.exportCsv.classList.toggle('cs-button--disabled', disabled);
  }

  function cell(row, text, className) {
    var td = document.createElement('td');
    td.textContent = text === undefined || text === null || text === '' ? '—' : String(text);
    if (className) {
      td.className = className;
    }
    row.appendChild(td);
    return td;
  }

  function renderItem(item) {
    var row = document.createElement('tr');
    cell(row, item.keyword);
    cell(row, item.title, 'cs-table__title');
    cell(row, item.filename, 'cs-table__mono');
    cell(row, item.list_size);
    cell(row, item.size);
    cell(row, item.date, 'cs-table__mono');
    cell(row, item.btih, 'cs-table__mono');

    var magnetCell = document.createElement('td');
    magnetCell.className = 'cs-table__magnet';
    var copy = document.createElement('button');
    copy.type = 'button';
    copy.className = 'cs-button cs-button--tiny';
    copy.textContent = '复制';
    copy.addEventListener('click', function () {
      copyText(item.magnet, copy);
    });
    magnetCell.appendChild(copy);
    row.appendChild(magnetCell);

    els.body.appendChild(row);
  }

  function appendItem(item) {
    if (!item || !item.magnet) {
      return;
    }
    items.push(item);
    renderItem(item);
    els.done.textContent = String(items.length);
    updateSummary();
  }

  function copyText(text, trigger) {
    function done() {
      if (!trigger) {
        return;
      }
      var original = trigger.textContent;
      trigger.textContent = '已复制';
      window.setTimeout(function () {
        trigger.textContent = original;
      }, 1200);
    }

    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done, function () {
        legacyCopy(text);
        done();
      });
      return;
    }
    legacyCopy(text);
    done();
  }

  function legacyCopy(text) {
    var area = document.createElement('textarea');
    area.value = text;
    area.setAttribute('readonly', 'readonly');
    area.style.position = 'fixed';
    area.style.top = '-1000px';
    document.body.appendChild(area);
    area.select();
    try {
      document.execCommand('copy');
    } catch (err) {
      showError('复制失败，请手动选中磁链复制');
    }
    document.body.removeChild(area);
  }

  function handleStatus(data) {
    if (data.job_id) {
      jobId = data.job_id;
    }
    setState(data.state);
    els.stage.textContent = STAGE_TEXT[data.stage] || data.stage || '—';
    els.keyword.textContent = data.keyword || '—';
    els.done.textContent = String(data.done || 0);
    els.total.textContent = String(data.total || 0);
    setNote(data.note);
    setRunning(data.state === 'running');
  }

  function handleDone(data) {
    if (data.job_id) {
      jobId = data.job_id;
    }
    setState(data.state || 'done');

    els.delay.textContent = '—';
    if (typeof data.count === 'number') {
      els.done.textContent = String(data.count);
    }
    if (typeof data.total === 'number') {
      els.total.textContent = String(data.total);
    }
    setRunning(false);
  }

  function connect() {
    if (source) {
      return;
    }
    source = new EventSource('/api/events');

    source.addEventListener('status', function (event) {
      handleStatus(JSON.parse(event.data));
    });

    source.addEventListener('delay', function (event) {
      var data = JSON.parse(event.data);
      setDelay(Number(data.seconds) || 0, data.reason);
    });

    source.addEventListener('item', function (event) {
      appendItem(JSON.parse(event.data));
    });

    source.addEventListener('done', function (event) {
      handleDone(JSON.parse(event.data));
    });

    source.onerror = function () {
      if (running) {
        setNote('实时连接中断，正在重连…');
      }
    };
  }

  function parseKeywords(raw) {
    return raw
      .split(/[\n,，、;；]+/)
      .map(function (part) {
        return part.trim();
      })
      .filter(function (part) {
        return part.length > 0;
      });
  }

  function submitJob(event) {
    event.preventDefault();
    showError('');

    var keywords = parseKeywords(els.keywords.value);
    var hot = Number(els.hot.value) || 0;
    var limit = Number(els.limit.value) || 0;
    var payload = {
      keywords: keywords,
      hot: keywords.length > 0 ? 0 : hot,
      limit: limit,
      detail: els.detail.checked
    };

    els.submit.disabled = true;

    fetch('/api/job', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    })
      .then(function (response) {
        return response.json().catch(function () {
          return {};
        }).then(function (body) {
          return { status: response.status, body: body };
        });
      })
      .then(function (result) {
        if (result.status === 201) {
          jobId = result.body.job_id || '';
          items = [];
          els.body.textContent = '';
          els.keyword.textContent = '—';
          els.stage.textContent = '—';
          els.delay.textContent = '—';
          setNote('');
          updateSummary();
          setState('running');
          setRunning(true);
          return;
        }
        if (result.status === 409) {
          jobId = result.body.job_id || '';
          setState('running');
          setRunning(true);
          showError(ERROR_TEXT.job_running);
          return;
        }
        showError(ERROR_TEXT[result.body.error] || '提交失败（HTTP ' + result.status + '）');
        setRunning(false);
      })
      .catch(function () {
        showError('提交失败，请检查服务是否可用');
        setRunning(false);
      });
  }

  function stopJob() {
    if (!jobId) {
      return;
    }
    els.stop.disabled = true;
    fetch('/api/job/' + encodeURIComponent(jobId) + '/stop', { method: 'POST' })
      .then(function (response) {
        return response.json();
      })
      .then(function (body) {
        if (!body.stopped) {
          setRunning(false);
        }
      })
      .catch(function () {
        els.stop.disabled = false;
        showError('停止请求失败，请重试');
      });
  }

  els.form.addEventListener('submit', submitJob);
  els.stop.addEventListener('click', stopJob);
  els.copyAll.addEventListener('click', function () {
    var magnets = items.map(function (item) {
      return item.magnet;
    });
    if (magnets.length === 0) {
      return;
    }
    copyText(magnets.join('\n'), els.copyAll);
  });

  updateSummary();
  connect();
})();