(() => {
  document.querySelectorAll("form[data-confirm]").forEach((form) => {
    form.addEventListener("submit", (event) => {
      const message = form.getAttribute("data-confirm");
      if (message && !window.confirm(message)) {
        event.preventDefault();
      }
    });
  });

  // Интервал при ошибке не может превышать основной интервал: держим max
  // синхронно с ним, чтобы браузер ловил ошибку до отправки формы.
  document.querySelectorAll("input[data-max-from]").forEach((input) => {
    const source = document.querySelector(
      `input[name="${input.getAttribute("data-max-from")}"]`
    );
    if (!source) {
      return;
    }
    const sync = () => {
      const limit = parseInt(source.value, 10);
      if (!Number.isFinite(limit) || limit < 1) {
        return;
      }
      input.max = String(limit);
      if (parseInt(input.value, 10) > limit) {
        input.value = String(limit);
      }
    };
    source.addEventListener("input", sync);
    source.addEventListener("change", sync);
    sync();
  });

  const canvas = document.getElementById("latency-chart");
  if (!canvas) {
    return;
  }

  const src = canvas.getAttribute("data-src");
  if (!src) {
    return;
  }

  fetch(src, { credentials: "same-origin" })
    .then((res) => {
      if (!res.ok) {
        throw new Error("failed to load chart data");
      }
      return res.json();
    })
    .then((points) => {
      const labels = points.map((p) => p.t);
      const values = points.map((p) => p.ms);
      const okFlags = points.map((p) => p.ok !== false);
      const pointColors = okFlags.map((ok) => (ok ? "#2563eb" : "#dc2626"));
      const pointRadius = okFlags.map((ok) => {
        if (!ok) {
          return 3;
        }
        return labels.length > 80 ? 0 : 2;
      });
      if (window.Chart) {
        const Chart = window.Chart;
        new Chart(canvas, {
          type: "line",
          data: {
            labels,
            datasets: [
              {
                label: "Время ответа, мс",
                data: values,
                borderColor: "#2563eb",
                backgroundColor: "rgba(37, 99, 235, 0.12)",
                fill: true,
                tension: 0.25,
                spanGaps: true,
                pointBackgroundColor: pointColors,
                pointBorderColor: pointColors,
                pointRadius,
              },
            ],
          },
          options: {
            responsive: true,
            maintainAspectRatio: false,
            plugins: {
              legend: { display: true },
            },
            scales: {
              y: { beginAtZero: true },
            },
          },
        });
        return;
      }
      drawFallbackChart(canvas, labels, values);
    })
    .catch((err) => {
      console.error(err);
    });

  function drawFallbackChart(el, labels, values) {
    const ctx = el.getContext("2d");
    const width = el.parentElement.clientWidth || 600;
    const height = 280;
    el.width = width;
    el.height = height;
    ctx.fillStyle = "#ffffff";
    ctx.fillRect(0, 0, width, height);
    if (!values.length) {
      ctx.fillStyle = "#667085";
      ctx.fillText("Нет данных для графика", 16, 24);
      return;
    }
    const max = Math.max(...values, 1);
    const pad = 32;
    ctx.strokeStyle = "#2563eb";
    ctx.lineWidth = 2;
    ctx.beginPath();
    values.forEach((v, i) => {
      const x = pad + (i * (width - pad * 2)) / Math.max(values.length - 1, 1);
      const y = height - pad - (v / max) * (height - pad * 2);
      if (i === 0) {
        ctx.moveTo(x, y);
      } else {
        ctx.lineTo(x, y);
      }
    });
    ctx.stroke();
    ctx.fillStyle = "#667085";
    ctx.fillText("0", 8, height - pad);
    ctx.fillText(String(max), 8, pad);
    if (labels.length) {
      ctx.fillText(labels[0], pad, height - 8);
      ctx.fillText(labels[labels.length - 1], width - 80, height - 8);
    }
  }
})();
