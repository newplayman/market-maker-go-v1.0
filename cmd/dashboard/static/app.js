let activityChart = null;
let priceChart = null;

document.addEventListener('DOMContentLoaded', () => {
    initCharts();
    loadConfig();
    startPolling();
});

function initCharts() {
    // Activity Chart
    const ctxActivity = document.getElementById('chart-activity').getContext('2d');
    activityChart = new Chart(ctxActivity, {
        type: 'line',
        data: {
            labels: [],
            datasets: [{
                label: 'Orders/Min',
                data: [],
                borderColor: '#3b82f6',
                tension: 0.4,
                pointRadius: 0
            }]
        },
        options: {
            responsive: true,
            maintainAspectRatio: false,
            scales: {
                y: { beginAtZero: true, grid: { color: '#334155' } },
                x: { display: false }
            },
            plugins: { legend: { display: false } },
            animation: false
        }
    });

    // Price Chart
    const ctxPrice = document.getElementById('chart-price').getContext('2d');
    priceChart = new Chart(ctxPrice, {
        type: 'line',
        data: {
            labels: [],
            datasets: [
                {
                    label: 'Price',
                    data: [],
                    borderColor: '#22c55e',
                    tension: 0.1,
                    pointRadius: 0
                },
                {
                    label: 'Entry Cost',
                    data: [],
                    borderColor: '#f59e0b',
                    borderDash: [5, 5],
                    tension: 0,
                    pointRadius: 0
                }
            ]
        },
        options: {
            responsive: true,
            maintainAspectRatio: false,
            scales: {
                y: { grid: { color: '#334155' } },
                x: { display: false }
            },
            plugins: { legend: { display: true, labels: { color: '#94a3b8' } } },
            animation: false
        }
    });
}

function startPolling() {
    setInterval(fetchStats, 1000);
    setInterval(fetchEvents, 1000);
    setInterval(fetchTrades, 5000);
    setInterval(fetchStatus, 2000);
}

async function fetchTrades() {
    try {
        const res = await fetch('/api/history/trades?limit=50');
        const trades = await res.json();
        const tbody = document.querySelector('#tradeTable tbody');

        if (!tbody) return;

        tbody.innerHTML = trades.map(t => {
            const time = new Date(t.timestamp * 1000).toLocaleTimeString();
            const pnlColor = t.pnl >= 0 ? '#22c55e' : '#ef4444';
            return `
                <tr style="border-bottom: 1px solid rgba(255,255,255,0.05);">
                    <td style="padding: 8px;">${time}</td>
                    <td style="padding: 8px;">${t.symbol}</td>
                    <td style="padding: 8px; color: ${t.side === 'BUY' ? '#22c55e' : '#ef4444'}">${t.side}</td>
                    <td style="padding: 8px;">${t.price.toFixed(2)}</td>
                    <td style="padding: 8px;">${t.quantity.toFixed(4)}</td>
                    <td style="padding: 8px; color: ${pnlColor}">${t.pnl.toFixed(4)}</td>
                </tr>
            `;
        }).join('');
    } catch (e) {
        console.error('Failed to fetch trades', e);
    }
}

async function fetchStats() {
    try {
        const res = await fetch('/api/stats');
        const stats = await res.json();

        // Update Financials
        document.getElementById('val-net-value').textContent = '$' + stats.net_value.toFixed(2);
        document.getElementById('val-total-pnl').textContent = '$' + stats.total_pnl.toFixed(2);
        document.getElementById('val-total-pnl').style.color = stats.total_pnl >= 0 ? '#22c55e' : '#ef4444';
        document.getElementById('val-position').textContent = stats.position_size.toFixed(4);
        document.getElementById('val-entry-price').textContent = '$' + stats.entry_price.toFixed(2);

        // Update Activity
        document.getElementById('val-active-orders').textContent = stats.active_orders;
        document.getElementById('val-orders-min').textContent = stats.orders_per_min.toFixed(1);
        document.getElementById('val-total-placed').textContent = stats.total_placed;
        document.getElementById('val-total-canceled').textContent = stats.total_canceled;

        // Update Charts
        const now = new Date().toLocaleTimeString();

        // Activity Chart
        if (activityChart.data.labels.length > 60) {
            activityChart.data.labels.shift();
            activityChart.data.datasets[0].data.shift();
        }
        activityChart.data.labels.push(now);
        activityChart.data.datasets[0].data.push(stats.orders_per_min);
        activityChart.update('none');

        // Price Chart
        if (priceChart.data.labels.length > 60) {
            priceChart.data.labels.shift();
            priceChart.data.datasets[0].data.shift();
            priceChart.data.datasets[1].data.shift();
        }
        priceChart.data.labels.push(now);
        priceChart.data.datasets[0].data.push(stats.current_price);
        priceChart.data.datasets[1].data.push(stats.entry_price > 0 ? stats.entry_price : null);
        priceChart.update('none');

    } catch (e) {
        console.error('Failed to fetch stats', e);
    }
}

async function fetchEvents() {
    try {
        const res = await fetch('/api/events');
        const events = await res.json();
        const logContainer = document.getElementById('event-log');

        // Simple diffing: clear and rebuild for now (can be optimized)
        logContainer.innerHTML = events.map(e => {
            const time = new Date(e.time).toLocaleTimeString();
            return `<div class="log-entry">
                <span class="log-time">[${time}]</span>
                <span class="log-type-${e.type}">${e.type}</span>
                <span class="log-msg">${e.message}</span>
            </div>`;
        }).reverse().join('');
    } catch (e) {
        console.error('Failed to fetch events', e);
    }
}

async function fetchStatus() {
    try {
        const res = await fetch('/api/status');
        const status = await res.json();
        const indicator = document.getElementById('status-indicator');
        const pidDisplay = document.getElementById('pid-display');

        if (status.running) {
            indicator.textContent = 'RUNNING';
            indicator.className = 'status running';
            pidDisplay.textContent = `PID: ${status.pid}`;
        } else {
            indicator.textContent = 'STOPPED';
            indicator.className = 'status stopped';
            pidDisplay.textContent = 'PID: -';
        }
    } catch (e) {
        console.error('Failed to fetch status', e);
    }
}

async function startProcess() {
    if (!confirm('Start trading process?')) return;
    await fetch('/api/start', { method: 'POST' });
    setTimeout(fetchStatus, 1000);
}

async function stopProcess() {
    if (!confirm('Stop trading process?')) return;
    await fetch('/api/stop', { method: 'POST' });
    setTimeout(fetchStatus, 1000);
}

async function loadConfig() {
    const res = await fetch('/api/config');
    const text = await res.text();
    document.getElementById('config-editor').value = text;
}

async function saveConfig() {
    if (!confirm('Save configuration? This may require a restart.')) return;
    const content = document.getElementById('config-editor').value;
    await fetch('/api/config', {
        method: 'POST',
        body: content
    });
    alert('Configuration saved.');
}
