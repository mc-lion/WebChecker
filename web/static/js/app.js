(() => {
  document.querySelectorAll("form[data-confirm]").forEach((form) => {
    form.addEventListener("submit", (event) => {
      const message = form.getAttribute("data-confirm");
      if (message && !window.confirm(message)) {
        event.preventDefault();
      }
    });
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
                pointRadius: labels.length > 80 ? 0 : 2,
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
