
(function() {

  function initControls() {
    window.setInterval(updateTimers, 1000);

    // init tooltips
    var tooltipEls = document.querySelectorAll('[data-bs-toggle="tooltip"]');
    Array.prototype.forEach.call(tooltipEls, function(tooltipEl) {
      new bootstrap.Tooltip(tooltipEl)
    });

    // init clipboard buttons
    var clipboard = new ClipboardJS("[data-clipboard-text]");
    clipboard.on("success", function (e) {
      var title = e.trigger.getAttribute("data-bs-original-title");
      var tooltip = bootstrap.Tooltip.getInstance(e.trigger);
      tooltip.setContent({ '.tooltip-inner': 'Copied!' });
      tooltip.show();
      setTimeout(function () {
        tooltip.setContent({ '.tooltip-inner': title });
      }, 1000);
    });
    clipboard.on("error", function (e) {
      var title = e.trigger.getAttribute("data-bs-original-title");
      var tooltip = bootstrap.Tooltip.getInstance(e.trigger);
      tooltip.setContent({ '.tooltip-inner': 'Failed to Copy!' });
      tooltip.show();
      setTimeout(function () {
        tooltip.setContent({ '.tooltip-inner': title });
      }, 1000);
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

  window.addEventListener('DOMContentLoaded', function() {
    initControls();
  });
})()
