
(function() {
  window.addEventListener('DOMContentLoaded', function() {
    initControls();
    modalFixes();
    window.setInterval(updateTimers, 1000);
    initHeaderSearch();
    initAddrHighlight();
  });
  var tooltipDict = {};
  var tooltipIdx = 1;
  // Offset in milliseconds: serverTime - clientTime. Used to correct relative-time
  // rendering when the user's local clock drifts from the server clock.
  var serverTimeOffsetMs = 0;
  function applyServerTime(serverMs) {
    if (!isFinite(serverMs) || serverMs <= 0) return;
    serverTimeOffsetMs = serverMs - Date.now();
    if (Math.abs(serverTimeOffsetMs) > 10000) {
      console.warn("Local clock drift detected: " + Math.round(serverTimeOffsetMs / 1000) + "s vs server. Relative times are corrected.");
    }
  }
  (function initServerTimeOffset() {
    var meta = document.querySelector('meta[name="server-time"]');
    if (!meta) return;
    applyServerTime(parseInt(meta.getAttribute("content"), 10));
  })();
  function serverNow() { return Date.now() + serverTimeOffsetMs; }
  function updateServerTime(serverMs) {
    applyServerTime(parseInt(serverMs, 10));
  }
  window.explorer = {
    initControls: initControls,
    renderRecentTime: renderRecentTime,
    tooltipDict: tooltipDict,
    refreshPeerInfos: refreshPeerInfos,
    hexToDecimal: hexToDecimal,
    checkRefreshCooldown: checkRefreshCooldown,
    serverNow: serverNow,
    updateServerTime: updateServerTime,
    ensNameFor: ensNameFor,
    ensEntriesFor: ensEntriesFor,
    applyEnsToNode: applyEnsToNode,
  };

  function modalFixes() {
    // Fix bootstrap backdrop stacking when having multiple modals
    $(document).on('show.bs.modal', '.modal', function() {
      const offset = (10 * $('.modal:visible').length);
      const zIndex = 2000 + offset;
      $(this).css('z-index', zIndex);
      setTimeout(() => $('.modal-backdrop').not('.modal-stack').css('z-index', zIndex - offset - 1).addClass('modal-stack'));
    });
    // Fix bootstrap scrolling stacking when having multiple modals
    $(document).on('hidden.bs.modal', '.modal', function(){
      $('.modal:visible').length && $(document.body).addClass('modal-open')
    });
  }

  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, function(c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  // ensNamesMap holds the merged address->entries lookup from every `.ens-names` JSON
  // block in the DOM (the layout carries one; lazy-loaded fragments carry their own).
  // Each entry is a list of {name, network, local} objects in display order (local
  // network first). Plain-string values (from stale cached page fragments using the
  // old single-name format) are normalized into the list form.
  var ensNamesMap = {};
  function refreshEnsNamesMap() {
    document.querySelectorAll('script.ens-names').forEach(function(blob) {
      try {
        var parsed = JSON.parse(blob.textContent || '{}');
        if (parsed && typeof parsed === 'object') {
          for (var key in parsed) {
            var value = parsed[key];
            if (typeof value === 'string') {
              value = value ? [{ name: value, network: '', local: true }] : [];
            }
            if (Array.isArray(value) && value.length > 0) {
              ensNamesMap[key.toLowerCase()] = value;
            }
          }
        }
      } catch (e) { /* ignore malformed block */ }
    });
  }
  function ensEntriesFor(address) {
    return address ? (ensNamesMap[String(address).toLowerCase()] || null) : null;
  }
  function ensNameFor(address) {
    var entries = ensEntriesFor(address);
    return entries && entries.length > 0 ? entries[0].name : null;
  }

  // setEnsTooltip sets a node's tooltip to "<name><br><address>" using Bootstrap's
  // data-bs-title (NOT the native `title`, which would show a second browser tooltip that
  // renders the <br> literally). Any already-initialized tooltip is disposed so it is
  // recreated with the ENS content instead of keeping the stale address-only text.
  function setEnsTooltip(node, name, addr) {
    var existing = bootstrap.Tooltip.getInstance(node);
    if (existing) existing.dispose();
    var oldIdx = node.getAttribute('data-tooltip-idx');
    if (oldIdx && tooltipDict[oldIdx]) delete tooltipDict[oldIdx];
    $(node).removeData('tooltip-init');
    node.removeAttribute('data-tooltip-idx');
    node.removeAttribute('title');
    node.removeAttribute('data-bs-original-title');
    node.setAttribute('data-bs-toggle', 'tooltip');
    node.setAttribute('data-bs-html', 'true');
    node.setAttribute('data-bs-title', escapeHtml(name) + '<br>' + String(addr).toLowerCase());
  }

  // attachEnsIcon inserts the clickable ENS icon (tag = resolved on the local network,
  // globe = resolved on a remote network) before a name-swapped node. Clicking it opens
  // the ENS callout with the raw address and all resolved names. A stale icon from a
  // previous swap (reused nodes in client-rendered callouts) is replaced.
  function attachEnsIcon(node, address, entries) {
    if (!node.parentNode) return;
    var prev = node.previousElementSibling;
    if (prev && prev.classList.contains('ens-icon')) {
      if (prev.getAttribute('data-ens-address') === address) return;
      var stalePopover = bootstrap.Popover.getInstance(prev);
      if (stalePopover) stalePopover.dispose();
      prev.remove();
    }
    var icon = document.createElement('span');
    icon.className = 'ens-icon ' + (entries[0].local ? 'ens-icon-local' : 'ens-icon-remote');
    icon.setAttribute('role', 'button');
    icon.setAttribute('tabindex', '0');
    icon.setAttribute('data-ens-address', address);
    icon.innerHTML = '<i class="fas ' + (entries[0].local ? 'fa-tag' : 'fa-globe') + '"></i>';
    node.parentNode.insertBefore(icon, node);
  }

  // applyEnsToNode swaps a single element's text for the primary ENS name of `address`
  // (if any), adding the ellipsis class, a full-name+address tooltip and the callout
  // icon. Copy/href stay untouched. Returns true when a name was applied. Used for
  // client-rendered callouts.
  function applyEnsToNode(node, address) {
    if (!node) return false;
    var entries = ensEntriesFor(address);
    if (!entries) return false;
    node.textContent = entries[0].name;
    node.classList.add('ens-name');
    setEnsTooltip(node, entries[0].name, address);
    attachEnsIcon(node, String(address).toLowerCase(), entries);
    return true;
  }

  // applyEnsNames swaps the displayed text of address elements for their resolved ENS
  // name. It merges every `.ens-names` block (so it works after lazy content is injected)
  // and is idempotent (skips already-swapped nodes) and re-runnable — it is called from
  // initControls, which runs again after lazy content loads. href/clipboard stay raw.
  function applyEnsNames() {
    refreshEnsNamesMap();
    if (Object.keys(ensNamesMap).length === 0) return;

    document.querySelectorAll('.ens-addr[data-address]').forEach(function(node) {
      if (node.getAttribute('data-ens-applied')) return;
      var addr = (node.getAttribute('data-address') || '').toLowerCase();
      var entries = ensNamesMap[addr];
      if (!entries) return;
      node.textContent = entries[0].name;
      node.classList.add('ens-name');
      setEnsTooltip(node, entries[0].name, addr);
      attachEnsIcon(node, addr, entries);
      node.setAttribute('data-ens-applied', '1');
    });
  }

  // The ENS callout: a popover on the `.ens-icon` badge showing the raw address and
  // every resolved name with its network, each copyable. One delegated listener covers
  // JS-injected icons and server-rendered ones (address page); only one callout is open
  // at a time and any outside click closes it.
  var openEnsPopover = null;
  function closeEnsPopover() {
    if (!openEnsPopover) return;
    try { openEnsPopover.hide(); } catch (e) { /* element may be gone */ }
    openEnsPopover = null;
  }
  function buildEnsCalloutContent(address) {
    var copyIcon = function(text) {
      return '<i class="fa fa-copy text-muted p-1" role="button" data-bs-toggle="tooltip" title="Copy to clipboard" data-clipboard-text="' + escapeHtml(text) + '"></i>';
    };
    var rows = ['<div class="ens-callout-row"><span class="ens-callout-value">' + escapeHtml(address) + '</span>' + copyIcon(address) + '</div>'];
    (ensEntriesFor(address) || []).forEach(function(entry) {
      rows.push('<div class="ens-callout-row"><span class="ens-callout-value">' + escapeHtml(entry.name) + '</span>' +
        '<span class="badge rounded-pill ' + (entry.local ? 'text-bg-primary' : 'text-bg-secondary') + '">' + escapeHtml(entry.network || 'local') + '</span>' +
        copyIcon(entry.name) + '</div>');
    });
    return rows.join('');
  }
  document.addEventListener('click', function(ev) {
    var icon = ev.target.closest ? ev.target.closest('.ens-icon[data-ens-address]') : null;
    if (!icon) {
      if (openEnsPopover && !(ev.target.closest && ev.target.closest('.ens-callout-popover'))) closeEnsPopover();
      return;
    }
    ev.preventDefault();
    ev.stopPropagation();
    var popover = bootstrap.Popover.getOrCreateInstance(icon, {
      html: true,
      title: 'ENS Names',
      content: ' ',
      trigger: 'manual',
      container: 'body',
      customClass: 'ens-callout-popover',
    });
    if (openEnsPopover === popover) {
      closeEnsPopover();
      return;
    }
    closeEnsPopover();
    popover.show();
    var body = popover.tip && popover.tip.querySelector('.popover-body');
    if (body) {
      body.innerHTML = buildEnsCalloutContent((icon.getAttribute('data-ens-address') || '').toLowerCase());
      initControls();
    }
    openEnsPopover = popover;
  });

  function initControls() {
    // swap addresses for ENS names before tooltips are initialized
    applyEnsNames();
    // init tooltips:
    // NOTE: `data-bs-toogle="tooltip"`` tooltips will also get cleaned up if their relevant element is removed from the DOM
    document.querySelectorAll('[data-bs-toggle="tooltip"]').forEach(initTooltip);
    cleanupTooltips();
    // NOTE: `data-toogle="tooltip"` tooltips will not get cleaned up if they are removed from the DOM
    $('[data-toggle="tooltip"]').tooltip()

    // init clipboard buttons
    var clipboard = new ClipboardJS('[data-clipboard-text], [data-clipboard-target]');
    clipboard.on("success", onClipboardSuccess);
    clipboard.on("error", onClipboardError);

    fitMethodBadges();
  }

  // fitMethodBadges shrinks the font of method badges whose label overflows the
  // capped width, so long method names stay on one line before the CSS ellipsis
  // kicks in.
  function fitMethodBadges() {
    document.querySelectorAll('.method-badge').forEach(function(el) {
      el.classList.remove('method-badge-sm');
      if (el.scrollWidth > el.clientWidth + 1) {
        el.classList.add('method-badge-sm');
      }
    });
  }

  // initAddrHighlight highlights every link to the address being hovered, so the same
  // account can be followed through a page that mentions it in several places - the
  // frames of a transaction, its state changes, the row it came from. It prefers the
  // nearest list (an EL data table or the internal-tx tree) and otherwise takes the
  // whole page, since an address that appears twice on a page is the same account
  // wherever it appears. Uses event delegation so lazily loaded content is covered.
  function initAddrHighlight() {
    var current = null;
    function clear() {
      if (!current) return;
      document.querySelectorAll('a.addr-hl').forEach(function(el) { el.classList.remove('addr-hl'); });
      current = null;
    }
    document.addEventListener('mouseover', function(ev) {
      var a = ev.target.closest ? ev.target.closest('a[href^="/address/0x"]') : null;
      if (!a) { return; }
      var scope = a.closest('.el-data-table, .itx-wrap') || document.body;
      var href = a.getAttribute('href');
      if (href === current) { return; }
      clear();
      current = href;
      scope.querySelectorAll('a[href="' + href + '"]').forEach(function(el) {
        el.classList.add('addr-hl');
      });
    });
    document.addEventListener('mouseout', function(ev) {
      var a = ev.target.closest ? ev.target.closest('a[href^="/address/0x"]') : null;
      if (a) { clear(); }
    });
  }

  function initTooltip(el) {
    if($(el).data("tooltip-init"))
      return;
    //console.log("init tooltip", el);
    var idx = tooltipIdx++;
    $(el).data("tooltip-init", idx).attr("data-tooltip-idx", idx.toString());
    $(el).tooltip();
    var tooltip = bootstrap.Tooltip.getInstance(el);
    tooltipDict[idx] = {
      element: el,
      tooltip: tooltip,
    };
  }

  function cleanupTooltips() {
    Object.keys(explorer.tooltipDict).forEach(function(idx) {
      var ref = explorer.tooltipDict[idx];
      if(document.body.contains(ref.element)) return;
      ref.tooltip.dispose();
      delete explorer.tooltipDict[idx];
    });
  }

  function onClipboardSuccess(e) {
    var title = e.trigger.getAttribute("data-bs-original-title");
    var tooltip = bootstrap.Tooltip.getInstance(e.trigger);
    tooltip.setContent({ '.tooltip-inner': 'Copied!' });
    tooltip.show();
    setTimeout(function () {
      tooltip.setContent({ '.tooltip-inner': title });
    }, 1000);
  }

  function onClipboardError(e) {
    var title = e.trigger.getAttribute("data-bs-original-title");
    var tooltip = bootstrap.Tooltip.getInstance(e.trigger);
    tooltip.setContent({ '.tooltip-inner': 'Failed to Copy!' });
    tooltip.show();
    setTimeout(function () {
      tooltip.setContent({ '.tooltip-inner': title });
    }, 1000);
  }

  function hexToDecimal(hexValue) {
    if (typeof hexValue !== 'string') return '';
    var cleanHex = hexValue.replace(/^0x/i, '');
    var decimal = parseInt(cleanHex, 16);
    return isNaN(decimal) ? '' : decimal.toString();
  }

  function checkRefreshCooldown() {
    var refreshButton = $('i[onclick="refreshPeerInfos()"]');
    
    if (refreshButton.length === 0) return; // Button not found on this page
    
    // Determine the client type based on current URL
    var clientType = window.location.pathname.includes('/clients/execution') ? 'execution' : 'consensus';
    
    fetch(`/clients/${clientType}/refresh/status`)
      .then(response => response.json())
      .then(data => {
        if (data.cooldown_active) {
          // Hide button during cooldown
          refreshButton.hide();
          
          var cooldownMsg = `Refresh cooldown active - ${data.remaining_seconds}s remaining`;
          if (data.online_clients) {
            if (data.total_cooldown === 60 && data.online_clients * 3 > 60) {
              cooldownMsg += ` (${data.online_clients} clients × 3s, capped at 60s)`;
            } else {
              cooldownMsg += ` (${data.online_clients} clients × 3s)`;
            }
          }
          refreshButton.attr('title', cooldownMsg);
          
          // Update countdown every second
          var countdown = setInterval(() => {
            fetch(`/clients/${clientType}/refresh/status`)
              .then(response => response.json())
              .then(statusData => {
                if (!statusData.cooldown_active) {
                  // Cooldown ended - show button
                  clearInterval(countdown);
                  refreshButton.show();
                  refreshButton.removeClass('disabled').css({
                    'opacity': '1',
                    'cursor': 'pointer',
                    'pointer-events': 'auto'
                  });
                  refreshButton.removeClass('fa-clock-o').addClass('fa-refresh');
                  refreshButton.attr('title', 'Refresh peer information');
                } else {
                  // Update remaining time
                  var cooldownMsg = `Refresh cooldown active - ${statusData.remaining_seconds}s remaining`;
                  if (statusData.online_clients) {
                    if (statusData.total_cooldown === 60 && statusData.online_clients * 3 > 60) {
                      cooldownMsg += ` (${statusData.online_clients} clients × 3s, capped at 60s)`;
                    } else {
                      cooldownMsg += ` (${statusData.online_clients} clients × 3s)`;
                    }
                  }
                  refreshButton.attr('title', cooldownMsg);
                }
              })
              .catch(() => {
                // On error, clear interval and reset button
                clearInterval(countdown);
                refreshButton.removeClass('disabled').css({
                  'opacity': '1',
                  'cursor': 'pointer',
                  'pointer-events': 'auto'
                });
                refreshButton.removeClass('fa-clock-o').addClass('fa-refresh');
                refreshButton.attr('title', 'Refresh peer information');
              });
          }, 1000);
        } else {
          // Button is available - show it
          refreshButton.show();
          refreshButton.removeClass('disabled').css({
            'opacity': '1',
            'cursor': 'pointer',
            'pointer-events': 'auto'
          });
          refreshButton.removeClass('fa-clock-o').addClass('fa-refresh');
          refreshButton.attr('title', 'Refresh peer information');
        }
      })
      .catch(error => {
        // On error, assume button is available - show it
        console.warn('Failed to check refresh cooldown status:', error);
        refreshButton.show();
        refreshButton.removeClass('disabled').css({
          'opacity': '1',
          'cursor': 'pointer',
          'pointer-events': 'auto'
        });
        refreshButton.removeClass('fa-clock-o').addClass('fa-refresh');
        refreshButton.attr('title', 'Refresh peer information');
      });
  }

  function updateTimers() {
    var timerEls = document.querySelectorAll("[data-timer]");
    timerEls.forEach(function(timerEl) {
      var time = timerEl.getAttribute("data-timer");
      var textEls = Array.prototype.filter.call(timerEl.querySelectorAll("*"), function(el) { return el.firstChild && el.firstChild.nodeType === 3 });
      var textEl = textEls.length ? textEls[0] : timerEl;

      textEl.innerText = renderRecentTime(time);
    });
  }

  function renderRecentTime(time) {
    var duration = time - Math.floor(serverNow() / 1000);
    var timeStr= "";
    var absDuration = Math.abs(duration);

    if (absDuration < 1) {
      return "now";
    } else if (absDuration < 60) {
      timeStr = absDuration + " sec."
    } else if (absDuration < 60*60) {
      timeStr = (Math.floor(absDuration / 60)) + " min."
    } else if (absDuration < 24*60*60) {
      timeStr = (Math.floor(absDuration / (60 * 60))) + " hr."
    } else {
      timeStr = (Math.floor(absDuration / (60 * 60 * 24))) + " day."
    }
    if (duration < 0) {
      return timeStr + " ago";
    } else {
      return "in " + timeStr;
    }
  }

  function initHeaderSearch() {
    var searchEl = jQuery("#explorer-search");
    let requestNum = 9
    var executionIndexerEnabled = searchEl.data("execution-indexer-enabled") === true || searchEl.attr("data-execution-indexer-enabled") === "true" || searchEl.data("executionIndexerEnabled") === true;
    var ensSearchEnabled = searchEl.data("ens-search-enabled") === true || searchEl.attr("data-ens-search-enabled") === "true" || searchEl.data("ensSearchEnabled") === true;

    var prepareQueryFn = function(query, settings) {
      settings.url += encodeURIComponent(query);
      return settings;
    }

    var bhSlots = new Bloodhound({
      datumTokenizer: Bloodhound.tokenizers.whitespace,
      queryTokenizer: Bloodhound.tokenizers.whitespace,
      identify: function (obj) {
        return obj.slot
      },
      remote: {
        url: "/search/slots?q=",
        prepare: prepareQueryFn,
        maxPendingRequests: requestNum,
      },
    });
    var bhExecBlocks = new Bloodhound({
      datumTokenizer: Bloodhound.tokenizers.whitespace,
      queryTokenizer: Bloodhound.tokenizers.whitespace,
      identify: function (obj) {
        return obj.slot
      },
      remote: {
        url: "/search/execblocks?q=",
        prepare: prepareQueryFn,
        maxPendingRequests: requestNum,
      },
    });
    var bhEpochs = new Bloodhound({
      datumTokenizer: Bloodhound.tokenizers.whitespace,
      queryTokenizer: Bloodhound.tokenizers.whitespace,
      identify: function (obj) {
        return obj.epoch
      },
      remote: {
        url: "/search/epochs?q=",
        prepare: prepareQueryFn,
        maxPendingRequests: requestNum,
      },
    });
    var bhGraffiti = new Bloodhound({
      datumTokenizer: Bloodhound.tokenizers.whitespace,
      queryTokenizer: Bloodhound.tokenizers.whitespace,
      identify: function (obj) {
        return obj.graffiti
      },
      remote: {
        url: "/search/graffiti?q=",
        prepare: prepareQueryFn,
        maxPendingRequests: requestNum,
      },
    });
    var bhValNames = new Bloodhound({
      datumTokenizer: Bloodhound.tokenizers.whitespace,
      queryTokenizer: Bloodhound.tokenizers.whitespace,
      identify: function (obj) {
        return obj.name
      },
      remote: {
        url: "/search/valname?q=",
        prepare: prepareQueryFn,
        maxPendingRequests: requestNum,
      },
    });
    var bhValidators = new Bloodhound({
      datumTokenizer: Bloodhound.tokenizers.whitespace,
      queryTokenizer: Bloodhound.tokenizers.whitespace,
      identify: function (obj) {
        return obj.index
      },
      remote: {
        url: "/search/validator?q=",
        prepare: prepareQueryFn,
        maxPendingRequests: requestNum,
      },
    });
    var bhAddresses = null;
    var bhTransactions = null;
    if (executionIndexerEnabled) {
      bhAddresses = new Bloodhound({
        datumTokenizer: Bloodhound.tokenizers.whitespace,
        queryTokenizer: Bloodhound.tokenizers.whitespace,
        identify: function (obj) {
          return obj.address
        },
        remote: {
          url: "/search/addresses?q=",
          prepare: prepareQueryFn,
          maxPendingRequests: requestNum,
        },
      });
      bhTransactions = new Bloodhound({
        datumTokenizer: Bloodhound.tokenizers.whitespace,
        queryTokenizer: Bloodhound.tokenizers.whitespace,
        identify: function (obj) {
          return obj.tx_hash
        },
        remote: {
          url: "/search/transactions?q=",
          prepare: prepareQueryFn,
          maxPendingRequests: requestNum,
        },
      });
    }

    var bhEnsNames = null;
    if (ensSearchEnabled) {
      bhEnsNames = new Bloodhound({
        datumTokenizer: Bloodhound.tokenizers.whitespace,
        queryTokenizer: Bloodhound.tokenizers.whitespace,
        identify: function (obj) {
          // the same name can resolve on multiple networks
          return obj.ens_name + "@" + obj.network
        },
        remote: {
          url: "/search/ens?q=",
          prepare: prepareQueryFn,
          maxPendingRequests: requestNum,
        },
      });
    }

    // Build datasets array conditionally
    var datasets = [
      {
        limit: 5,
        name: "slot",
        source: bhSlots,
        display: "root",
        templates: {
          header: '<h3 class="h5">Slots:</h3>',
          suggestion: function (data) {
            var status = "";
            if (data.orphaned) {
              status = `<span class="search-cell"><span class="badge rounded-pill text-bg-info">Orphaned</span></span>`;
            }
            return `<div class="text-monospace"><div class="search-table"><span class="search-cell">${data.slot}:</span><span class="search-cell search-truncate">${data.root}</span>${status}</div></div>`;
          },
        },
      },
      {
        limit: 5,
        name: "execblocks",
        source: bhExecBlocks,
        display: "root",
        templates: {
          header: '<h3 class="h5">Slots (by execution block):</h3>',
          suggestion: function (data) {
            var status = "";
            if (data.orphaned) {
              status = `<span class="search-cell"><span class="badge rounded-pill text-bg-info">Orphaned</span></span>`;
            }
            return `<div class="text-monospace"><div class="search-table"><span class="search-cell">${data.slot}:</span><span class="search-cell search-truncate"><nobr>Block ${data.exec_number} / ${data.exec_hash}</nobr></span>${status}</div></div>`;
          },
        },
      },
      {
        limit: 5,
        name: "name",
        source: bhValNames,
        display: "name",
        templates: {
          header: '<h3 class="h5">Slots (by validator name):</h3>',
          suggestion: function (data) {
            return `<div class="text-monospace" style="display:flex"><div class="text-truncate" style="flex:1 1 auto;">${data.name}</div><div style="max-width:fit-content;white-space:nowrap;">${data.count}</div></div>`
          },
        },
      },
      {
        limit: 5,
        name: "epoch",
        source: bhEpochs,
        display: "epoch",
        templates: {
          header: '<h3 class="h5">Epochs:</h3>',
          suggestion: function (data) {
            return `<div class="text-monospace">${data.epoch}</div>`
          },
        },
      },
      {
        limit: 5,
        name: "graffiti",
        source: bhGraffiti,
        display: "graffiti",
        templates: {
          header: '<h3 class="h5">Blocks (by graffitis):</h3>',
          suggestion: function (data) {
            return `<div class="text-monospace" style="display:flex"><div class="text-truncate" style="flex:1 1 auto;">${data.graffiti}</div><div style="max-width:fit-content;white-space:nowrap;">${data.count}</div></div>`
          },
        },
      },
      {
        limit: 5,
        name: "validator",
        source: bhValidators,
        display: "index",
        templates: {
          header: '<h3 class="h5">Validators:</h3>',
          suggestion: function (data) {
            var nameDisplay = data.name ? `<span class="text-muted" style="white-space:nowrap"> (${data.name})</span>` : '';
            return `<div class="text-monospace"><div class="search-table"><span class="search-cell">${data.index}:</span><span class="search-cell search-truncate">${data.pubkey}</span>${nameDisplay}</div></div>`;
          },
        },
      }
    ];

    // Add execution indexer datasets conditionally
    if (executionIndexerEnabled && bhAddresses && bhTransactions) {
      datasets.push({
        limit: 5,
        name: "address",
        source: bhAddresses,
        display: "address",
        templates: {
          header: '<h3 class="h5">Addresses:</h3>',
          suggestion: function (data) {
            var badges = "";
            if (data.is_contract) {
              badges += `<span class="search-cell"><span class="badge rounded-pill text-bg-warning">Contract</span></span>`;
            }
            if (!data.has_data) {
              badges += `<span class="search-cell"><span class="badge rounded-pill text-bg-secondary">New</span></span>`;
            }
            return `<div class="text-monospace"><div class="search-table"><span class="search-cell search-truncate">${data.address}</span>${badges}</div></div>`;
          },
        },
      });
      datasets.push({
        limit: 5,
        name: "transaction",
        source: bhTransactions,
        display: "tx_hash",
        templates: {
          header: '<h3 class="h5">Transactions:</h3>',
          suggestion: function (data) {
            var status = "";
            if (data.reverted) {
              status = `<span class="search-cell"><span class="badge rounded-pill text-bg-danger">Failed</span></span>`;
            }
            var blockInfo = data.block_number ? `<span class="search-cell text-muted">Block ${data.block_number}</span>` : "";
            return `<div class="text-monospace"><div class="search-table"><span class="search-cell search-truncate">${data.tx_hash}</span>${blockInfo}${status}</div></div>`;
          },
        },
      });
    }

    // Add ENS dataset conditionally
    if (ensSearchEnabled && bhEnsNames) {
      datasets.push({
        limit: 5,
        name: "ens",
        source: bhEnsNames,
        display: "ens_name",
        templates: {
          header: '<h3 class="h5">ENS Names:</h3>',
          suggestion: function (data) {
            // ens_name is server-side html-escaped; network is a trusted config value
            var badges = `<span class="search-cell"><span class="badge rounded-pill ${data.local ? "text-bg-primary" : "text-bg-secondary"}">${data.network}</span></span>`;
            if (data.is_contract) {
              badges += `<span class="search-cell"><span class="badge rounded-pill text-bg-warning">Contract</span></span>`;
            }
            return `<div class="text-monospace"><div class="search-table"><span class="search-cell">${data.ens_name}</span><span class="search-cell search-truncate text-muted">${data.address}</span>${badges}</div></div>`;
          },
        },
      });
    }

    // Initialize typeahead with all datasets
    searchEl.typeahead.apply(searchEl, [
      {
        minLength: 1,
        highlight: true,
        hint: false,
        autoselect: false,
      }
    ].concat(datasets))

    searchEl.on("input", function (input) {
      $(".tt-suggestion").first().addClass("tt-cursor")
    })

    jQuery(".tt-menu").on("mouseenter", function () {
      $(".tt-suggestion").first().removeClass("tt-cursor")
    })

    jQuery(".tt-menu").on("mouseleave", function () {
      $(".tt-suggestion").first().addClass("tt-cursor")
    })

    searchEl.on("typeahead:select", function (ev, sug) {
      if (sug.root !== undefined) {
        if (sug.orphaned) {
          window.location = "/slot/" + sug.root
        } else {
          window.location = "/slot/" + sug.slot
        }
      } else if (sug.epoch !== undefined) {
        window.location = "/epoch/" + sug.epoch
      } else if (sug.graffiti !== undefined) {
        // sug.graffiti is html-escaped to prevent xss, we need to unescape it
        var el = document.createElement("textarea")
        el.innerHTML = sug.graffiti
        window.location = "/slots/filtered?f&f.orphaned=1&f.graffiti=" + encodeURIComponent(el.value)
      } else if (sug.pubkey !== undefined) {
        window.location = "/validator/" + sug.index
      } else if (sug.ens_name !== undefined) {
        window.location = "/address/" + sug.address
      } else if (sug.name !== undefined) {
          // sug.name is html-escaped to prevent xss, we need to unescape it
          var el = document.createElement("textarea")
          el.innerHTML = sug.name
          window.location = "/slots/filtered?f&f.missing=1&f.orphaned=1&f.pname=" + encodeURIComponent(el.value)
      } else if (sug.address !== undefined) {
        window.location = "/address/" + sug.address
      } else if (sug.tx_hash !== undefined) {
        window.location = "/tx/" + sug.tx_hash
      } else {
        console.log("invalid typeahead-selection", sug)
      }
    })
  }

  function refreshPeerInfos() {
    var refreshButton = $('i[onclick="refreshPeerInfos()"]');
    
    // Check if button is disabled due to cooldown
    if (refreshButton.hasClass('disabled') || refreshButton.css('pointer-events') === 'none') {
      return; // Don't allow refresh during cooldown
    }
    
    // Disable button and show spinning icon
    refreshButton.addClass('disabled').css({
      'opacity': '0.7',
      'pointer-events': 'none'
    });
    refreshButton.removeClass('fa-refresh fa-clock-o').addClass('fa-refresh fa-spin');
    
    // Determine the client type based on current URL
    var clientType = window.location.pathname.includes('/clients/execution') ? 'execution' : 'consensus';
    
    // Call the refresh API
    fetch(`/clients/${clientType}/refresh`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
    })
    .then(response => response.json())
    .then(data => {
      if (data.success) {
        // Success - show success message briefly then reload
        refreshButton.removeClass('fa-spin fa-refresh').addClass('fa-check text-success');
        refreshButton.attr('title', `Successfully refreshed ${data.refreshed_clients} clients`);
        setTimeout(() => {
          window.location.reload();
        }, 1000);
      } else {
        // Error - show message and re-enable button or start cooldown
        refreshButton.removeClass('fa-spin').addClass('fa-refresh');
        
        if (data.message && data.message.includes('cooldown')) {
          // Handle cooldown - start checking cooldown status
          checkRefreshCooldown();
        } else {
          // Other error - re-enable button
          refreshButton.removeClass('disabled').css({
            'opacity': '1',
            'pointer-events': 'auto'
          });
          alert('Failed to refresh peer information: ' + (data.message || 'Unknown error'));
        }
      }
    })
    .catch(error => {
      // Network error - show message and re-enable button
      refreshButton.removeClass('fa-spin').addClass('fa-refresh');
      refreshButton.removeClass('disabled').css({
        'opacity': '1',
        'pointer-events': 'auto'
      });
      alert('Failed to refresh peer information: ' + error.message);
    });
  }
})()

window.refreshPeerInfos = function() {
  var refreshButton = $('i[onclick="refreshPeerInfos()"]');
  
  // Check if button is disabled due to cooldown
  if (refreshButton.hasClass('disabled') || refreshButton.css('pointer-events') === 'none') {
    return; // Don't allow refresh during cooldown
  }
  
  // Disable button and show spinning icon
  refreshButton.addClass('disabled').css({
    'opacity': '0.7',
    'pointer-events': 'none'
  });
  refreshButton.removeClass('fa-refresh fa-clock-o').addClass('fa-refresh fa-spin');
  
  // Determine the client type based on current URL
  var clientType = window.location.pathname.includes('/clients/execution') ? 'execution' : 'consensus';
  
  // Call the refresh API
  fetch(`/clients/${clientType}/refresh`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
  })
  .then(response => response.json())
  .then(data => {
    if (data.success) {
      // Success - show success message briefly then reload
      refreshButton.removeClass('fa-spin fa-refresh').addClass('fa-check text-success');
      refreshButton.attr('title', `Successfully refreshed ${data.refreshed_clients} clients`);
      setTimeout(() => {
        window.location.reload();
      }, 1000);
    } else {
      // Error - show message and re-enable button or start cooldown
      refreshButton.removeClass('fa-spin').addClass('fa-refresh');
      
      if (data.message && data.message.includes('cooldown')) {
        // Handle cooldown - start checking cooldown status
        window.explorer.checkRefreshCooldown();
      } else {
        // Other error - re-enable button
        refreshButton.removeClass('disabled').css({
          'opacity': '1',
          'pointer-events': 'auto'
        });
        alert('Failed to refresh peer information: ' + (data.message || 'Unknown error'));
      }
    }
  })
  .catch(error => {
    // Network error - show message and re-enable button
    refreshButton.removeClass('fa-spin').addClass('fa-refresh');
    refreshButton.removeClass('disabled').css({
      'opacity': '1',
      'pointer-events': 'auto'
    });
    alert('Failed to refresh peer information: ' + error.message);
  });
};
