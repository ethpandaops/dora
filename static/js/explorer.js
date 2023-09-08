
(function() {
  window.addEventListener('DOMContentLoaded', function() {
    initControls();
    window.setInterval(updateTimers, 1000);
    initHeaderSearch();
  });
  var tooltipDict = {};
  var tooltipIdx = 1;
  window.explorer = {
    initControls: initControls,
    renderRecentTime: renderRecentTime,
    tooltipDict: tooltipDict,
  };

  function initControls() {
    // init tooltips
    document.querySelectorAll('[data-bs-toggle="tooltip"]').forEach(initTooltip);
    cleanupTooltips();

    // init clipboard buttons
    document.querySelectorAll("[data-clipboard-text]").forEach(initCopyBtn);
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

  function initCopyBtn(el) {
    if($(el).data("clipboard-init"))
      return;
    $(el).data("clipboard-init", true);
    var clipboard = new ClipboardJS(el);
    clipboard.on("success", onClipboardSuccess);
    clipboard.on("error", onClipboardError);
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
    var duration = time - Math.floor(new Date().getTime() / 1000);
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


    searchEl.typeahead(
      {
        minLength: 1,
        highlight: true,
        hint: false,
        autoselect: false,
      },
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
      }
    )
  
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
      } else if (sug.name !== undefined) {
          // sug.name is html-escaped to prevent xss, we need to unescape it
          var el = document.createElement("textarea")
          el.innerHTML = sug.name
          window.location = "/slots/filtered?f&f.missing=1&f.orphaned=1&f.pname=" + encodeURIComponent(el.value)
      } else {
        console.log("invalid typeahead-selection", sug)
      }
    })
  }

  
})()
