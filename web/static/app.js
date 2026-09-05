(() => {
  "use strict";

  // Must match internal/match/evidence.go's Max*Score constants —
  // the breakdown's component denominators, not independently tuned.
  const MAX_AMOUNT_SCORE = 40;
  const MAX_DATE_SCORE = 15;
  const MAX_REFERENCE_SCORE = 30;
  const MAX_DESCRIPTION_SCORE = 10;
  const MAX_UNIQUENESS_SCORE = 5;

  const REDUCE_MOTION = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  // One small icon per severity, reused everywhere a status/category
  // badge appears — never color alone, and never a different icon set
  // per table. currentColor so each icon inherits its badge's text color.
  const SEVERITY_ICONS = {
    good: '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6"><circle cx="8" cy="8" r="6.4"/><path d="M5.1 8.3l1.9 1.9 4-4.4" stroke-linecap="round" stroke-linejoin="round"/></svg>',
    warning: '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M8 2.6l6.1 10.6H1.9z" stroke-linejoin="round"/><path d="M8 6.6v3" stroke-linecap="round"/><circle cx="8" cy="11.3" r="0.9" fill="currentColor" stroke="none"/></svg>',
    serious: '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6"><circle cx="8" cy="8" r="6.4"/><path d="M6.1 6.4a1.9 1.9 0 113.2 1.4c-.6.5-1.1.8-1.1 1.7" stroke-linecap="round" stroke-linejoin="round"/><circle cx="8" cy="11.4" r="0.9" fill="currentColor" stroke="none"/></svg>',
    critical: '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6"><circle cx="8" cy="8" r="6.4"/><path d="M5.6 5.6l4.8 4.8M10.4 5.6l-4.8 4.8" stroke-linecap="round"/></svg>',
    neutral: '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6"><circle cx="8" cy="8" r="6.4"/><path d="M5.3 8h5.4" stroke-linecap="round"/></svg>',
  };

  function iconSpan(severityKey) {
    const svg = SEVERITY_ICONS[severityKey];
    if (!svg) return null;
    const span = document.createElement("span");
    span.className = "badge-icon";
    span.innerHTML = svg;
    span.setAttribute("aria-hidden", "true");
    return span;
  }

  const runBtn = document.getElementById("run-btn");
  const statusLine = document.getElementById("status-line");
  const emptyState = document.getElementById("empty-state");
  const resultsSection = document.getElementById("results");
  const errorBanner = document.getElementById("error-banner");

  const sizePicker = document.getElementById("size-picker");
  const generateBtn = document.getElementById("generate-btn");
  const generateProgress = document.getElementById("generate-progress");
  const generateProgressLabel = document.getElementById("generate-progress-label");
  const generateProgressBar = document.getElementById("generate-progress-bar");
  const generateSteps = document.getElementById("generate-steps");
  const generatePreview = document.getElementById("generate-preview");
  const previewSeed = document.getElementById("preview-seed");
  const previewTables = document.getElementById("preview-tables");

  const uploadForm = document.getElementById("upload-form");
  const uploadBtn = document.getElementById("upload-btn");

  const summaryEl = document.getElementById("summary");
  const funnelEl = document.getElementById("funnel");
  const gtMissesPanel = document.getElementById("gt-misses-panel");
  const gtMissesCount = document.getElementById("gt-misses-count");
  const gtMissesBody = document.querySelector("#gt-misses-table tbody");
  const exceptionsBody = document.querySelector("#exceptions-table tbody");
  const exceptionsCount = document.getElementById("exceptions-count");
  const exceptionsEmpty = document.getElementById("exceptions-empty");
  const transactionsBody = document.querySelector("#transactions-table tbody");
  const transactionsCount = document.getElementById("transactions-count");
  const transactionsToggle = document.getElementById("transactions-toggle");
  const transactionsBodyWrap = document.getElementById("transactions-body-wrap");
  const filterRow = document.getElementById("decision-filter");

  const drawer = document.getElementById("drawer");
  const drawerBackdrop = document.getElementById("drawer-backdrop");
  const drawerTitle = document.getElementById("drawer-title");
  const drawerBody = document.getElementById("drawer-body");
  const drawerClose = document.getElementById("drawer-close");

  let lastResults = [];
  let lastExceptions = [];
  let lastVerdictByOrder = {};
  let activeFilter = "ALL";
  let transactionsExpanded = false;

  function formatRupees(paise) {
    const rupees = (paise || 0) / 100;
    return "₹" + rupees.toLocaleString("en-IN", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
  }

  function formatPercent(value) {
    if (value === null || value === undefined) return "—";
    return value.toFixed(1) + "%";
  }

  // animateCount counts el's text from `from` to `to` over `duration`ms,
  // easing out, formatted by `formatter(value)` at every frame. Skips
  // straight to the final value under prefers-reduced-motion.
  function animateCount(el, from, to, formatter, duration = 700) {
    if (REDUCE_MOTION) {
      el.textContent = formatter(to);
      return;
    }
    const start = performance.now();
    function frame(now) {
      const t = Math.min(1, (now - start) / duration);
      const eased = 1 - Math.pow(1 - t, 3);
      el.textContent = formatter(from + (to - from) * eased);
      if (t < 1) requestAnimationFrame(frame);
    }
    requestAnimationFrame(frame);
  }

  // gauge builds a small radial progress ring — track + animated fill
  // arc — with the numeric value always printed alongside it separately
  // (renderSummary puts the counted-up number in stat-value text), never
  // relying on the ring's fill length or color as the only signal.
  function gauge(percent, colorClass, size, stroke) {
    size = size || 56;
    stroke = stroke || 6;
    const clamped = Math.max(0, Math.min(100, percent || 0));
    const r = (size - stroke) / 2;
    const c = 2 * Math.PI * r;
    const center = size / 2;

    const wrap = document.createElement("div");
    wrap.className = "stat-gauge";
    wrap.innerHTML =
      '<svg width="' + size + '" height="' + size + '" viewBox="0 0 ' + size + " " + size + '" aria-hidden="true">' +
        '<circle class="gauge-track" cx="' + center + '" cy="' + center + '" r="' + r + '" stroke-width="' + stroke + '" fill="none"/>' +
        '<circle class="gauge-fill ' + colorClass + '" cx="' + center + '" cy="' + center + '" r="' + r +
          '" stroke-width="' + stroke + '" fill="none" stroke-linecap="round" stroke-dasharray="' + c +
          '" stroke-dashoffset="' + c + '" transform="rotate(-90 ' + center + " " + center + ')"/>' +
      "</svg>";

    const fill = wrap.querySelector(".gauge-fill");
    const targetOffset = c * (1 - clamped / 100);
    if (REDUCE_MOTION) {
      fill.style.strokeDashoffset = targetOffset;
    } else {
      requestAnimationFrame(() => requestAnimationFrame(() => { fill.style.strokeDashoffset = targetOffset; }));
    }
    return wrap;
  }

  function decisionBadgeClass(decision) {
    switch (decision) {
      case "MATCHED": return "badge-good";
      case "DISCREPANCY": return "badge-warning";
      case "AMBIGUOUS": return "badge-serious";
      case "UNRESOLVED": return "badge-critical";
      default: return "badge-warning";
    }
  }

  function categoryBadgeClass(action) {
    switch (action) {
      case "RECHECK": return "badge-warning";
      case "VERIFY_FEE": return "badge-warning";
      case "MANUAL_REVIEW": return "badge-serious";
      case "ESCALATE": return "badge-critical";
      default: return "badge-warning";
    }
  }

  function badge(text, cls) {
    const span = document.createElement("span");
    span.className = "badge " + cls;
    const icon = iconSpan(cls.replace("badge-", ""));
    if (icon) span.appendChild(icon);
    const label = document.createElement("span");
    label.textContent = text;
    span.appendChild(label);
    return span;
  }

  function td(content) {
    const cell = document.createElement("td");
    if (content instanceof Node) {
      cell.appendChild(content);
    } else {
      cell.textContent = content ?? "";
    }
    return cell;
  }

  // collapsibleText condenses a long reason/note into a one-line
  // summary that expands to the full paragraph on click — a native
  // <details>/<summary> disclosure, so no extra JS state to track and
  // it stays keyboard/screen-reader accessible for free. Short text
  // (the common case) is returned as a plain text node, unchanged.
  function collapsibleText(text, maxLen) {
    maxLen = maxLen || 90;
    if (!text) return document.createTextNode("");
    if (text.length <= maxLen) return document.createTextNode(text);

    let cut = text.slice(0, maxLen);
    const lastSpace = cut.lastIndexOf(" ");
    if (lastSpace > maxLen * 0.6) cut = cut.slice(0, lastSpace);

    const details = document.createElement("details");
    details.className = "text-disclosure";
    const summary = document.createElement("summary");
    summary.textContent = cut.trim() + "…";
    const full = document.createElement("p");
    full.textContent = text;
    details.appendChild(summary);
    details.appendChild(full);
    return details;
  }

  function setStatus(text) {
    statusLine.textContent = text;
  }

  function showError(message) {
    errorBanner.textContent = message;
    errorBanner.hidden = false;
  }

  function clearError() {
    errorBanner.hidden = true;
    errorBanner.textContent = "";
  }

  function renderSummary(summary) {
    // AI escalation rate is the one tile where *lower* is the good
    // direction — it gets the warning ring color deliberately, not the
    // shared accent, so its gauge never reads as "more is better" like
    // the other three (CLAUDE.md Phase 11a's inverted-semantics note).
    const tiles = [
      { label: "Match rate", value: summary.match_rate, sub: summary.matched_count + " / " + summary.total_orders + " orders", gauge: "gauge-accent" },
      { label: "Value reconciled", value: summary.value_reconciled_rate, sub: "by ₹ amount, not record count", gauge: "gauge-accent" },
      { label: "AI escalation rate", value: summary.ai_escalation_rate, sub: "sent to the Gemini judge", gauge: "gauge-warning" },
      { label: "Ground-truth accuracy", value: summary.ground_truth_accuracy, sub: summary.ground_truth_accuracy != null ? summary.ground_truth_evaluated + " orders scored" : "no ground truth supplied", gauge: "gauge-accent" },
    ];

    summaryEl.innerHTML = "";
    for (const tile of tiles) {
      const hasValue = tile.value !== null && tile.value !== undefined;

      const el = document.createElement("div");
      el.className = "stat-tile";
      el.appendChild(gauge(hasValue ? tile.value : 0, hasValue ? tile.gauge : "gauge-neutral"));

      const textWrap = document.createElement("div");
      textWrap.className = "stat-text";
      const labelEl = document.createElement("p");
      labelEl.className = "stat-label";
      labelEl.textContent = tile.label;
      const valueEl = document.createElement("p");
      valueEl.className = "stat-value";
      valueEl.textContent = hasValue ? "0.0%" : "—";
      const subEl = document.createElement("p");
      subEl.className = "stat-sub";
      subEl.textContent = tile.sub;
      textWrap.appendChild(labelEl);
      textWrap.appendChild(valueEl);
      textWrap.appendChild(subEl);
      el.appendChild(textWrap);

      summaryEl.appendChild(el);
      if (hasValue) animateCount(valueEl, 0, tile.value, (v) => v.toFixed(1) + "%");
    }
  }

  function renderFunnel(summary, results, exceptions) {
    const analyzed = summary.total_orders;
    const matched = summary.matched_count;
    const aiReviewed = results.filter((r) => r.decision === "AMBIGUOUS").length;
    const escalated = exceptions.filter((e) => e.action === "ESCALATE").length;

    const steps = [
      { label: "Analyzed", value: analyzed },
      { label: "Matched", value: matched },
      { label: "AI-reviewed", value: aiReviewed },
      { label: "Escalated", value: escalated },
    ];

    funnelEl.innerHTML = "";
    steps.forEach((step, i) => {
      const li = document.createElement("li");
      li.className = "funnel-step";
      const valueEl = document.createElement("span");
      valueEl.className = "funnel-value";
      valueEl.textContent = "0";
      const labelEl = document.createElement("span");
      labelEl.className = "funnel-label";
      labelEl.textContent = step.label;
      li.appendChild(valueEl);
      li.appendChild(labelEl);
      funnelEl.appendChild(li);
      animateCount(valueEl, 0, step.value, (v) => String(Math.round(v)));
      if (i < steps.length - 1) {
        const arrow = document.createElement("li");
        arrow.className = "funnel-arrow";
        arrow.setAttribute("aria-hidden", "true");
        arrow.textContent = "→";
        funnelEl.appendChild(arrow);
      }
    });
  }

  // "AI judge" as a bare label overclaims when the judge's actual output
  // was thrown out (network failure, or a rejected/malformed response) —
  // a reader skimming the table would reasonably assume the AI
  // contributed something useful. Distinguish that case explicitly.
  function sourceLabel(exc) {
    if (exc.source !== "AI_JUDGE") return "Deterministic";
    const verdict = lastVerdictByOrder[exc.order_id];
    return verdict && verdict.fallback ? "AI judge (unavailable)" : "AI judge";
  }

  function renderExceptions(exceptions) {
    exceptionsBody.innerHTML = "";
    exceptionsCount.textContent = exceptions.length + " of " + lastResults.length + " orders";
    exceptionsEmpty.hidden = exceptions.length > 0;

    for (const exc of exceptions) {
      const row = document.createElement("tr");
      row.className = "row-clickable";
      row.tabIndex = 0;
      row.dataset.orderId = exc.order_id;
      row.appendChild(td(exc.order_id));
      row.appendChild(td(badge(exc.category, categoryBadgeClass(exc.action))));
      row.appendChild(td(badge(exc.action, categoryBadgeClass(exc.action))));
      const valueCell = td(formatRupees(exc.value_at_risk));
      valueCell.classList.add("num");
      row.appendChild(valueCell);
      row.appendChild(td(sourceLabel(exc)));
      const reasonCell = td(collapsibleText(exc.reason || ""));
      reasonCell.classList.add("reason-cell");
      row.appendChild(reasonCell);
      if (!REDUCE_MOTION) row.classList.add("row-flash");
      exceptionsBody.appendChild(row);
    }
  }

  function updateTransactionsToggle(total) {
    transactionsToggle.textContent = transactionsExpanded ? "Hide transactions" : "Show all " + total;
  }

  function renderTransactions(results) {
    const filtered = activeFilter === "ALL" ? results : results.filter((r) => r.decision === activeFilter);
    transactionsCount.textContent = filtered.length + " of " + results.length + " orders shown";
    updateTransactionsToggle(results.length);

    transactionsBody.innerHTML = "";
    for (const r of filtered) {
      const row = document.createElement("tr");
      row.appendChild(td(r.order_id));
      row.appendChild(td(badge(r.decision, decisionBadgeClass(r.decision))));
      row.appendChild(td(r.rule || "—"));
      const expected = td(formatRupees(r.expected_net));
      expected.classList.add("num");
      row.appendChild(expected);
      const actual = td(formatRupees(r.actual_credit));
      actual.classList.add("num");
      row.appendChild(actual);
      const delta = td(formatRupees(r.delta));
      delta.classList.add("num");
      row.appendChild(delta);
      const notes = td(collapsibleText(r.notes || ""));
      notes.classList.add("notes-cell");
      row.appendChild(notes);
      if (!REDUCE_MOTION) row.classList.add("row-flash");
      transactionsBody.appendChild(row);
    }
  }

  // renderGroundTruthMisses lists the orders the pipeline got wrong
  // versus known ground truth — the named errors behind an accuracy
  // figure below 100%. Hidden entirely when accuracy is perfect or no
  // ground truth was supplied.
  function renderGroundTruthMisses(summary) {
    const misses = summary.ground_truth_misses || [];
    if (!misses.length) {
      gtMissesPanel.hidden = true;
      return;
    }
    gtMissesCount.textContent = misses.length + (misses.length === 1 ? " order" : " orders");
    gtMissesBody.innerHTML = "";
    for (const m of misses) {
      const row = document.createElement("tr");
      row.appendChild(td(m.order_id));
      row.appendChild(td(badge(m.got, decisionBadgeClass(m.got))));
      row.appendChild(td(badge(m.expected, decisionBadgeClass(m.expected))));
      gtMissesBody.appendChild(row);
    }
    gtMissesPanel.hidden = false;
  }

  function render(report) {
    lastResults = report.results || [];
    lastExceptions = report.exceptions || [];
    lastVerdictByOrder = {};
    for (const v of report.verdicts || []) {
      lastVerdictByOrder[v.order_id] = v;
    }
    renderSummary(report.summary);
    renderFunnel(report.summary, lastResults, lastExceptions);
    renderGroundTruthMisses(report.summary);
    renderExceptions(lastExceptions);
    renderTransactions(lastResults);

    emptyState.hidden = true;
    resultsSection.hidden = false;

    const when = new Date(report.generated_at);
    setStatus("Last run: " + when.toLocaleString());
  }

  // --- investigation drawer (Phase 10's GET /reconcile/orders/:id) ---

  function evidenceBar(label, percent, cls) {
    const wrap = document.createElement("div");
    wrap.className = "evidence-bar";
    wrap.innerHTML =
      '<div class="evidence-bar-head"><span>' + label + '</span><span class="evidence-bar-pct">' + percent.toFixed(0) + "%</span></div>" +
      '<div class="meter"><div class="meter-fill ' + cls + '" style="width:' + Math.max(0, Math.min(100, percent)) + '%"></div></div>';
    return wrap;
  }

  // Formats an evidence component value without a trailing ".0" for
  // whole numbers, but keeps one decimal place otherwise (Priority 3:
  // sub-scores are precise, not round coincidences).
  function formatComponent(n) {
    return Number.isInteger(n) ? String(n) : n.toFixed(1);
  }

  function componentBar(label, value, max) {
    const wrap = document.createElement("div");
    wrap.className = "evidence-bar";
    const pct = max > 0 ? (value / max) * 100 : 0;
    wrap.innerHTML =
      '<div class="evidence-bar-head"><span>' + label + '</span><span class="evidence-bar-pct">' + formatComponent(value) + "/" + max + "</span></div>" +
      '<div class="meter"><div class="meter-fill meter-evidence" style="width:' + Math.max(0, Math.min(100, pct)) + '%"></div></div>';
    return wrap;
  }

  // candidateBreakdown renders one candidate's full evidence
  // calculation (Priority 1) — the auditable math behind its single
  // evidence_score, not just the number itself.
  function candidateBreakdown(candidate) {
    const wrap = document.createElement("div");
    wrap.className = "candidate-row";
    const bd = candidate.breakdown;
    const header = document.createElement("div");
    header.className = "candidate-header";
    header.innerHTML =
      "<strong>" + candidate.utr + "</strong>" +
      '<span class="candidate-total">' + formatComponent(bd.total) + "/100</span>";
    wrap.appendChild(header);
    wrap.appendChild(componentBar("Amount match", bd.amount_score, MAX_AMOUNT_SCORE));
    wrap.appendChild(componentBar("Date match", bd.date_score, MAX_DATE_SCORE));
    wrap.appendChild(componentBar("Reference match", bd.reference_score, MAX_REFERENCE_SCORE));
    wrap.appendChild(componentBar("Description match", bd.description_score, MAX_DESCRIPTION_SCORE));
    wrap.appendChild(componentBar("Unique candidate", bd.uniqueness_score, MAX_UNIQUENESS_SCORE));
    return wrap;
  }

  function renderDrawerBody(trace) {
    drawerBody.innerHTML = "";
    const m = trace.match;
    const v = trace.verdict;
    const exc = trace.exception;

    // "AMBIGUOUS (NONE)" / "UNRESOLVED (NONE)" both read like a leaked
    // internal enum value — neither has a resolved Rule by definition,
    // so say why in plain language instead (Precision & Impressiveness
    // Pass, Priority 6, extended to cover UNRESOLVED too).
    let decisionLabel;
    if (m.decision === "AMBIGUOUS") {
      decisionLabel = "AMBIGUOUS — pending resolution";
    } else if (m.decision === "UNRESOLVED") {
      decisionLabel = "UNRESOLVED — no matching bank credit found";
    } else {
      decisionLabel = m.decision + " (" + (m.rule || "NONE") + ")";
    }

    const details = document.createElement("div");
    details.className = "drawer-section";
    details.innerHTML =
      "<p><strong>Decision:</strong> " + decisionLabel + "</p>" +
      "<p><strong>Expected net:</strong> " + formatRupees(m.expected_net) + "</p>";
    if (m.notes) {
      const notesP = document.createElement("p");
      notesP.className = "muted";
      notesP.appendChild(collapsibleText(m.notes));
      details.appendChild(notesP);
    }
    drawerBody.appendChild(details);

    if (m.rule_cascade && m.rule_cascade.length) {
      const cascadeSection = document.createElement("div");
      cascadeSection.className = "drawer-section";
      // Shown for every order, not just AI-escalated ones (e.g. ORD_1049
      // resolves deterministically via EXACT_REFERENCE and never reaches
      // the judge at all) — the heading stays decision-agnostic.
      cascadeSection.innerHTML = "<h3>Match reasoning</h3>";
      const list = document.createElement("ul");
      list.className = "cascade-list";
      const outcomeText = { resolved: "resolved", no_match: "no match", escalated: "escalated" };
      const outcomeIcon = { resolved: "good", no_match: "neutral", escalated: "serious" };
      for (const step of m.rule_cascade) {
        const li = document.createElement("li");
        const rule = document.createElement("span");
        rule.className = "cascade-rule";
        rule.textContent = step.rule;
        const outcome = document.createElement("span");
        outcome.className = "cascade-outcome cascade-outcome-" + step.outcome;
        const icon = iconSpan(outcomeIcon[step.outcome]);
        if (icon) outcome.appendChild(icon);
        const outcomeText_ = document.createElement("span");
        outcomeText_.textContent = outcomeText[step.outcome] || step.outcome;
        outcome.appendChild(outcomeText_);
        const detail = document.createElement("span");
        detail.className = "cascade-detail";
        detail.textContent = step.detail;
        li.appendChild(rule);
        li.appendChild(outcome);
        li.appendChild(detail);
        list.appendChild(li);
      }
      cascadeSection.appendChild(list);
      drawerBody.appendChild(cascadeSection);
    }

    if (exc) {
      const excSection = document.createElement("div");
      excSection.className = "drawer-section";
      excSection.innerHTML =
        "<h3>Exception</h3>" +
        "<p><strong>Category:</strong> " + exc.category + " &middot; <strong>Action:</strong> " + exc.action + "</p>" +
        "<p><strong>Value at risk:</strong> " + formatRupees(exc.value_at_risk) + "</p>";
      drawerBody.appendChild(excSection);
    }

    if (m.candidates && m.candidates.length) {
      const scoreSection = document.createElement("div");
      scoreSection.className = "drawer-section";
      scoreSection.innerHTML = "<h3>Evidence vs. AI confidence</h3>";
      const topScore = Math.max(...m.candidates.map((c) => c.evidence_score));
      scoreSection.appendChild(evidenceBar("Evidence score (deterministic)", topScore, "meter-evidence"));

      // Decision confidence is a distinct claim from either candidate's
      // own evidence_score: it measures the margin between the top two
      // candidates, not how strong either one looks in isolation. A
      // small margin correctly reads as low confidence even when both
      // candidates individually score reasonably — that's what should
      // drive MANUAL_REVIEW (Precision & Impressiveness Pass, Priority 2).
      const totals = m.candidates.map((c) => c.breakdown.total).sort((a, b) => b - a);
      const margin = totals.length > 1 ? totals[0] - totals[1] : null;
      const decisionConfEl = evidenceBar("Decision confidence (margin between top 2)", m.decision_confidence, "meter-decision");
      scoreSection.appendChild(decisionConfEl);
      if (margin !== null) {
        const marginNote = document.createElement("p");
        marginNote.className = "muted margin-note";
        marginNote.textContent = margin < 5
          ? "Candidates are within " + formatComponent(margin) + " points of each other — this is a genuine tie, not a close call."
          : "The leading candidate beats the runner-up by " + formatComponent(margin) + " points.";
        scoreSection.appendChild(marginNote);
      }

      // A fallback verdict (network failure, or a rejected malformed/
      // meaningless response — internal/judge/judge.go's ValidateRaw
      // requires confidence strictly > 0) always has confidence 0.
      // Rendering that as an empty 0% bar reads as a broken UI, not as
      // "no signal" — show why instead of a flat bar.
      if (v) {
        if (!v.fallback && v.confidence > 0) {
          scoreSection.appendChild(evidenceBar("Gemini confidence", v.confidence * 100, "meter-confidence"));
        } else {
          const unavailable = document.createElement("p");
          unavailable.className = "muted";
          unavailable.textContent = "Gemini confidence unavailable — the AI judge's output was rejected or unavailable for this order.";
          scoreSection.appendChild(unavailable);
        }
      }
      drawerBody.appendChild(scoreSection);
    }

    if (v) {
      const reasonSection = document.createElement("div");
      reasonSection.className = "drawer-section";
      if (v.fallback) {
        // Never say "falling back to UNRESOLVED" here — the header
        // already states the real, standing decision (whatever
        // match.Result.Decision says, e.g. "AMBIGUOUS — pending
        // resolution"). This section only explains what happened to
        // the AI *attempt*: it was thrown out, so the match-level
        // classification is what stands, not a second, contradicting
        // "UNRESOLVED" story.
        reasonSection.innerHTML =
          "<h3>AI verdict rejected</h3>" +
          "<p>Gemini's response was unavailable or failed validation, so it was discarded — deferring to manual review instead of trusting an unreadable or invalid answer.</p>";
        const detailP = document.createElement("p");
        detailP.className = "muted";
        detailP.appendChild(collapsibleText(v.reason || ""));
        reasonSection.appendChild(detailP);
      } else {
        reasonSection.innerHTML = "<h3>AI reasoning</h3>";
        const reasonP = document.createElement("p");
        reasonP.appendChild(collapsibleText(v.reason || ""));
        reasonSection.appendChild(reasonP);
        const riskP = document.createElement("p");
        riskP.className = "muted";
        riskP.innerHTML = "<strong>Risk if wrong:</strong> ";
        riskP.appendChild(collapsibleText(v.risk_if_wrong || ""));
        reasonSection.appendChild(riskP);
      }
      drawerBody.appendChild(reasonSection);
    }

    if (m.candidates && m.candidates.length) {
      const candSection = document.createElement("div");
      candSection.className = "drawer-section";
      candSection.innerHTML = "<h3>Candidates considered</h3>";
      for (const c of m.candidates) {
        candSection.appendChild(candidateBreakdown(c));
      }
      drawerBody.appendChild(candSection);
    }

    const historySection = document.createElement("div");
    historySection.className = "drawer-section";
    historySection.innerHTML = '<h3>Human decisions</h3><ul id="drawer-history-list" class="history-list"></ul>';
    drawerBody.appendChild(historySection);

    // One button per candidate — "Approve match" alone doesn't say
    // *which* UTR is being approved when there are multiple tied
    // candidates, and that's the actual decision being made
    // (Precision & Impressiveness Pass, Priority 5).
    const actions = document.createElement("div");
    actions.className = "drawer-section drawer-actions";
    if (m.candidates && m.candidates.length) {
      for (const c of m.candidates) {
        const btn = document.createElement("button");
        btn.type = "button";
        btn.className = "action-btn";
        btn.textContent = "Approve " + c.utr;
        btn.addEventListener("click", () => resolveOrder(trace.order_id, "APPROVED", c.utr));
        actions.appendChild(btn);
      }
    }
    const flagBtn = document.createElement("button");
    flagBtn.type = "button";
    flagBtn.className = "action-btn";
    flagBtn.textContent = "Flag for review";
    flagBtn.addEventListener("click", () => resolveOrder(trace.order_id, "FLAGGED", ""));
    actions.appendChild(flagBtn);
    drawerBody.appendChild(actions);

    loadResolutionHistory(trace.order_id);
  }

  // --- human override logging (Phase 12's POST/GET .../resolve) ---

  function renderResolutionHistory(resolutions) {
    const list = document.getElementById("drawer-history-list");
    if (!list) return; // drawer was closed/re-rendered before this resolved
    list.innerHTML = "";
    if (!resolutions.length) {
      const li = document.createElement("li");
      li.className = "muted";
      li.textContent = "No human decisions logged yet for this order.";
      list.appendChild(li);
      return;
    }
    for (const r of resolutions) {
      const li = document.createElement("li");
      const when = new Date(r.created_at).toLocaleString();
      const actionLabel = r.action + (r.utr ? " (" + r.utr + ")" : "");
      li.innerHTML =
        '<span class="badge ' + (r.action === "APPROVED" ? "badge-good" : "badge-warning") + '">' + actionLabel + "</span> " +
        "<span>" + (r.reason || "") + "</span>" +
        '<span class="history-time">' + when + "</span>";
      list.appendChild(li);
    }
  }

  async function loadResolutionHistory(orderId) {
    try {
      const res = await fetch("/reconcile/orders/" + encodeURIComponent(orderId) + "/resolutions");
      if (!res.ok) return;
      const resolutions = await res.json();
      renderResolutionHistory(resolutions || []);
    } catch {
      // history is a nice-to-have; leave the section as-is on failure
    }
  }

  async function resolveOrder(orderId, action, utr) {
    const reason = window.prompt("Reason for this decision (required):", "");
    if (reason === null) return; // cancelled
    // Reject an empty reason client-side, before it ever reaches the
    // API — a one-line reason is required for every action (Precision &
    // Impressiveness Pass, Priority 5).
    if (reason.trim() === "") {
      alert("A reason is required before this action can be logged.");
      return;
    }
    try {
      const res = await fetch("/reconcile/orders/" + encodeURIComponent(orderId) + "/resolve", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ action, reason: reason.trim(), utr: utr || "" }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body.error || ("request failed with status " + res.status));
      }
      await loadResolutionHistory(orderId);
    } catch (err) {
      alert("Could not log this decision: " + err.message);
    }
  }

  async function openDrawer(orderId) {
    drawerTitle.textContent = orderId;
    drawerBody.innerHTML = '<p class="muted">Loading…</p>';
    drawerBackdrop.hidden = false;
    drawer.hidden = false;
    try {
      const res = await fetch("/reconcile/orders/" + encodeURIComponent(orderId));
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body.error || ("request failed with status " + res.status));
      }
      const trace = await res.json();
      renderDrawerBody(trace);
    } catch (err) {
      drawerBody.innerHTML = '<p class="muted">Could not load this order: ' + err.message + "</p>";
    }
  }

  function closeDrawer() {
    drawer.hidden = true;
    drawerBackdrop.hidden = true;
  }

  async function runReconciliation() {
    runBtn.disabled = true;
    setStatus("Running reconciliation…");
    clearError();
    generatePreview.hidden = true;
    try {
      const res = await fetch("/reconcile/run");
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body.error || ("request failed with status " + res.status));
      }
      const report = await res.json();
      render(report);
    } catch (err) {
      showError("Reconciliation failed: " + err.message);
      setStatus("");
    } finally {
      runBtn.disabled = false;
    }
  }

  // --- generate dataset (Phase 16 Tier 1: judges bring their own data,
  // starting with a fresh synthetic batch rather than the fixed
  // fixtures) ---

  let selectedGenerateSize = 50;

  const GENERATE_STEPS = [
    "Generating synthetic transactions",
    "Running deterministic matching",
    "Sending ambiguous cases to AI judge",
    "Computing reconciliation metrics",
  ];

  function renderGenerateSteps(activeIndex) {
    generateSteps.innerHTML = "";
    GENERATE_STEPS.forEach((label, i) => {
      const li = document.createElement("li");
      if (i < activeIndex) li.className = "step-done";
      else if (i === activeIndex) li.className = "step-active";
      li.textContent = label;
      generateSteps.appendChild(li);
    });
  }

  // The backend runs this as one synchronous call — there's no real
  // incremental progress to report. This animates a plausible
  // indicative progress (capped short of 100% until the response
  // actually arrives, then completes) so a judge watching the demo sees
  // the pipeline visibly working rather than a frozen button, without
  // the UI claiming a false level of precision about server-side state.
  function runIndicativeProgress(size) {
    let stopped = false;
    let pct = 0;
    let stepIndex = 0;
    generateProgressLabel.textContent = "Generating " + size + " orders…";
    renderGenerateSteps(0);

    const stepTimer = setInterval(() => {
      stepIndex = Math.min(stepIndex + 1, GENERATE_STEPS.length - 1);
      renderGenerateSteps(stepIndex);
    }, 900);

    function tick() {
      if (stopped) return;
      // ease toward 90%, never claiming completion until the real
      // response arrives
      pct += (90 - pct) * 0.06;
      generateProgressBar.style.width = Math.min(pct, 90) + "%";
      generateProgressLabel.textContent = "Generating " + size + " orders… " + Math.round(Math.min(pct, 90)) + "%";
      if (!REDUCE_MOTION) requestAnimationFrame(tick);
    }
    if (REDUCE_MOTION) {
      generateProgressBar.style.width = "90%";
    } else {
      requestAnimationFrame(tick);
    }

    return {
      finish() {
        stopped = true;
        clearInterval(stepTimer);
        renderGenerateSteps(GENERATE_STEPS.length);
        generateProgressBar.style.width = "100%";
        generateProgressLabel.textContent = "Done";
      },
      stop() {
        stopped = true;
        clearInterval(stepTimer);
      },
    };
  }

  // miniTable renders a small labelled table from an array of row
  // objects, showing only the columns named in `cols` ([key, header]
  // pairs). Used for the post-generation sample preview.
  function miniTable(title, rows, cols) {
    const wrap = document.createElement("div");
    wrap.className = "preview-table";
    const h = document.createElement("h4");
    h.textContent = title;
    wrap.appendChild(h);
    const table = document.createElement("table");
    const thead = document.createElement("thead");
    const htr = document.createElement("tr");
    for (const [, header] of cols) {
      const th = document.createElement("th");
      th.textContent = header;
      htr.appendChild(th);
    }
    thead.appendChild(htr);
    table.appendChild(thead);
    const tbody = document.createElement("tbody");
    for (const row of rows || []) {
      const tr = document.createElement("tr");
      for (const [key] of cols) {
        const cell = document.createElement("td");
        const v = row[key];
        cell.textContent = typeof v === "number" ? formatRupees(v) : (v ?? "");
        tr.appendChild(cell);
      }
      tbody.appendChild(tr);
    }
    table.appendChild(tbody);
    wrap.appendChild(table);
    return wrap;
  }

  // renderGeneratePreview shows the seed used plus a few sample records
  // spanning ledger/gateway/bank — a visibly different seed number next
  // to visibly different records is checkable proof the data actually
  // changed, not just a percentage that clusters in the same range by
  // design.
  function renderGeneratePreview(generation) {
    if (!generation) {
      generatePreview.hidden = true;
      return;
    }
    previewSeed.textContent = String(generation.seed);
    previewTables.innerHTML = "";
    const s = generation.sample || {};
    previewTables.appendChild(miniTable("Ledger", s.ledger, [
      ["order_id", "Order"], ["customer", "Customer"], ["gross_amount", "Gross"], ["status", "Status"],
    ]));
    previewTables.appendChild(miniTable("Gateway", s.gateway, [
      ["payment_id", "Payment"], ["order_id", "Order"], ["fee", "Fee"], ["net_amount", "Net"],
    ]));
    previewTables.appendChild(miniTable("Bank", s.bank, [
      ["utr", "UTR"], ["date", "Date"], ["credit_amount", "Credit"], ["description", "Description"],
    ]));
    generatePreview.hidden = false;
  }

  async function generateDataset() {
    generateBtn.disabled = true;
    runBtn.disabled = true;
    clearError();
    generateProgress.hidden = false;
    generateProgressBar.style.width = "0%";
    generatePreview.hidden = true;
    const progress = runIndicativeProgress(selectedGenerateSize);

    try {
      // No seed param — the backend defaults to its nanosecond clock, so
      // every click is a genuinely different dataset. It returns the seed
      // it used, which renderGeneratePreview then shows.
      const res = await fetch("/reconcile/generate?size=" + selectedGenerateSize, { method: "POST" });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body.error || ("request failed with status " + res.status));
      }
      const report = await res.json();
      progress.finish();
      render(report);
      renderGeneratePreview(report.generation);
      setStatus("Generated " + selectedGenerateSize + " orders (seed " + report.generation.seed + ") — last run: " + new Date(report.generated_at).toLocaleString());
    } catch (err) {
      progress.stop();
      showError("Dataset generation failed: " + err.message);
    } finally {
      generateBtn.disabled = false;
      runBtn.disabled = false;
      setTimeout(() => { generateProgress.hidden = true; }, 1200);
    }
  }

  // --- bring your own data (Phase 16 Tier 2) ---

  async function uploadDataset(e) {
    e.preventDefault();
    const ledgerFile = document.getElementById("upload-ledger").files[0];
    const gatewayFile = document.getElementById("upload-gateway").files[0];
    const bankFile = document.getElementById("upload-bank").files[0];
    if (!ledgerFile || !gatewayFile || !bankFile) {
      showError("Ledger, gateway, and bank files are all required.");
      return;
    }

    const form = new FormData();
    form.append("ledger", ledgerFile);
    form.append("gateway", gatewayFile);
    form.append("bank", bankFile);

    uploadBtn.disabled = true;
    runBtn.disabled = true;
    clearError();
    generatePreview.hidden = true;
    setStatus("Uploading and reconciling…");
    try {
      const res = await fetch("/reconcile/upload", { method: "POST", body: form });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        // Surface the real ingest validation text verbatim (e.g. "ledger[3]
        // (order_id=\"ORD_4\"): gross_amount must be positive, got -500") —
        // this is the whole point of routing uploads through the existing,
        // unchanged ingest validators: a clear error instead of a crash.
        throw new Error(body.error || ("request failed with status " + res.status));
      }
      const report = await res.json();
      render(report);
      uploadForm.reset();
    } catch (err) {
      showError("Upload failed: " + err.message);
      setStatus("");
    } finally {
      uploadBtn.disabled = false;
      runBtn.disabled = false;
    }
  }

  async function loadExistingReport() {
    try {
      const res = await fetch("/reconcile/report");
      if (!res.ok) return; // 409: nothing run yet — leave the empty state showing
      const report = await res.json();
      render(report);
    } catch {
      // ignore — empty state stays
    }
  }

  filterRow.addEventListener("click", (e) => {
    const btn = e.target.closest(".filter-chip");
    if (!btn) return;
    activeFilter = btn.dataset.filter;
    for (const chip of filterRow.querySelectorAll(".filter-chip")) {
      chip.classList.toggle("is-active", chip === btn);
    }
    renderTransactions(lastResults);
  });

  transactionsToggle.addEventListener("click", () => {
    transactionsExpanded = !transactionsExpanded;
    transactionsBodyWrap.hidden = !transactionsExpanded;
    updateTransactionsToggle(lastResults.length);
  });

  // Expanding a "Show more" reason lives inside the same clickable row —
  // without this guard, that click bubbles up and also opens the
  // investigation drawer as an unwanted side effect.
  exceptionsBody.addEventListener("click", (e) => {
    if (e.target.closest(".text-disclosure")) return;
    const row = e.target.closest(".row-clickable");
    if (!row) return;
    openDrawer(row.dataset.orderId);
  });

  exceptionsBody.addEventListener("keydown", (e) => {
    if (e.target.closest(".text-disclosure")) return;
    if (e.key !== "Enter" && e.key !== " ") return;
    const row = e.target.closest(".row-clickable");
    if (!row) return;
    e.preventDefault();
    openDrawer(row.dataset.orderId);
  });

  drawerClose.addEventListener("click", closeDrawer);
  drawerBackdrop.addEventListener("click", closeDrawer);
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !drawer.hidden) closeDrawer();
  });

  runBtn.addEventListener("click", runReconciliation);

  sizePicker.addEventListener("click", (e) => {
    const btn = e.target.closest(".size-chip");
    if (!btn) return;
    selectedGenerateSize = parseInt(btn.dataset.size, 10);
    for (const chip of sizePicker.querySelectorAll(".size-chip")) {
      chip.classList.toggle("is-active", chip === btn);
    }
  });

  generateBtn.addEventListener("click", generateDataset);

  uploadForm.addEventListener("submit", uploadDataset);

  loadExistingReport();
})();
