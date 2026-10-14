# Foundational Statistics, Observability Mathematics & Time-Series Analysis

This document provides a comprehensive mathematical and practical guide to statistical analysis, latency distributions, percentiles, boxplots, moving averages, seasonality modeling, and relative anomaly detection for resilience engineering, QA, and telemetry architectures.

> 📊 **Interactive Visual Gallery**: All statistical distributions and models in this guide are available as standalone interactive Chart.js templates under [`doc/charts/`](./charts/index.html).

---

## Table of Contents

- [1. The Mathematical Failure of Naive Metrics (Min, Average, Max)](#1-the-mathematical-failure-of-naive-metrics-min-average-max)
  - [1.1 The Min-Time Fallacy: How Fast Errors Masquerade as Peak Performance](#11-the-min-time-fallacy-how-fast-errors-masquerade-as-peak-performance)
  - [1.2 The Average-Time Fallacy: Blended Meaninglessness in Multimodal Systems](#12-the-average-time-fallacy-blended-meaninglessness-in-multimodal-systems)
  - [1.3 The Max-Time Fallacy: Outlier Hysteria vs. Volume Blindness](#13-the-max-time-fallacy-outlier-hysteria-vs-volume-blindness)
  - [1.4 The Anatomy of Latency Distributions (PDF vs. CDF)](#14-the-anatomy-of-latency-distributions-pdf-vs-cdf)
- [2. Percentiles & Quantiles Explained ($p_{50}$ to $p_{99.99}$)](#2-percentiles--quantiles-explained-p_50-to-p_9999)
  - [2.1 Mathematical Definition of Quantiles](#21-mathematical-definition-of-quantiles)
  - [2.2 The Meaning of the "Nines" in Production Latency](#22-the-meaning-of-the-nines-in-production-latency)
  - [2.3 The Fatal Flaw: The Percentile Averaging Fallacy](#23-the-fatal-flaw-the-percentile-averaging-fallacy)
  - [2.4 Correct Cross-Node Percentile Aggregation (HdrHistogram, t-digest & DDSketch)](#24-correct-cross-node-percentile-aggregation-hdrhistogram-t-digest--ddsketch)
- [3. Boxplots & Dispersion Analysis (Tukey's Summary)](#3-boxplots--dispersion-analysis-tukeys-summary)
  - [3.1 The Five-Number Summary & The Interquartile Range (IQR)](#31-the-five-number-summary--the-interquartile-range-iqr)
  - [3.2 Outlier Classification ($1.5 \times \text{IQR}$ Rule)](#32-outlier-classification-15-times-textiqr-rule)
  - [3.3 ASCII Boxplot Reference & Interpretation](#33-ascii-boxplot-reference--interpretation)
- [4. Time Windows & Moving Averages: Sizing the "Last X over Y" Equation](#4-time-windows--moving-averages-sizing-the-last-x-over-y-equation)
  - [4.1 Sizing the "Last X over Y" Parameters for Observability Plots](#41-sizing-the-last-x-over-y-parameters-for-observability-plots)
  - [4.2 The 3 Critical Sizing Pitfalls (Aliasing, Flapping & Inertia)](#42-the-3-critical-sizing-pitfalls-aliasing-flapping--inertia)
  - [4.3 Fixed Windows vs. Sliding Continuous Windows](#43-fixed-windows-vs-sliding-continuous-windows)
  - [4.4 Simple Moving Average (SMA) vs. Cumulative Moving Average (CMA)](#44-simple-moving-average-sma-vs-cumulative-moving-average-cma)
  - [4.5 Exponential Weighted Moving Average (EWMA)](#45-exponential-weighted-moving-average-ewma)
- [5. Seasonality, Trends & Relative Anomaly Detection](#5-seasonality-trends--relative-anomaly-detection)
  - [5.1 Time-Series Decomposition: Trend, Seasonality & Residuals](#51-time-series-decomposition-trend-seasonality--residuals)
  - [5.2 Relative Anomaly Detection: X% Deviation from Previous Y Periods](#52-relative-anomaly-detection-x-deviation-from-previous-y-periods)
  - [5.3 Seasonally-Aware Prometheus Alerting (The `offset` Pattern)](#53-seasonally-aware-prometheus-alerting-the-offset-pattern)
  - [5.4 Holt-Winters Triple Exponential Smoothing](#54-holt-winters-triple-exponential-smoothing)

---

## 1. The Mathematical Failure of Naive Metrics (Min, Average, Max)

In production software operations, engineering teams frequently rely on three naive summary metrics: **Minimum Latency**, **Average Latency**, and **Maximum Latency**. In real-world distributed architectures, each of these metrics fails to represent system health and actively misleads decision-making.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    THE THREE NAIVE METRIC FALLACIES                         │
├────────────────────┬─────────────────────────────┬──────────────────────────┤
│ NAIVE METRIC       │ WHY IT FAILS IN REALITY     │ REAL-WORLD PRODUCTION BUG│
├────────────────────┼─────────────────────────────┼──────────────────────────┤
│ 1. Minimum Time    │ Measures fast rejections,   │ Database dies -> API fast│
│    (Min)           │ unauthenticated 401s, and   │ fails in 0.2ms -> Min    │
│                    │ empty cached 304 responses. │ looks "amazingly fast"!  │
├────────────────────┼─────────────────────────────┼──────────────────────────┤
│ 2. Average Time    │ Blends fast cache hits with │ 99 users get 10ms, 1 user│
│    (Mean)          │ catastrophic DB lock stalls │ hangs 30s -> Mean is     │
│                    │ into a fictional number.    │ 310ms (reflects NO user).│
├────────────────────┼─────────────────────────────┼──────────────────────────┤
│ 3. Maximum Time    │ Tracks a single extreme     │ One elevator WiFi drop   │
│    (Max)           │ outlier; blind to volume of │ sets Max to 60s; cannot  │
│                    │ affected users.             │ tell if 1 or 1,000 died. │
└────────────────────┴─────────────────────────────┴──────────────────────────┘
```

---

### 1.1 The Min-Time Fallacy: How Fast Errors Masquerade as Peak Performance
- **The Failure Mode**: The fastest response a web server can possibly generate is an **immediate rejection**.
- **Real-World Scenario**: If your primary database crashes, your Go HTTP handler immediately returns `500 Internal Error` or `503 Service Unavailable` in $0.2\text{ms}$ without parsing the database or executing business logic. 
- **The Dashboard Illusion**: The "Minimum Response Time" metric drops from $15\text{ms}$ to $0.2\text{ms}$. A naive monitoring dashboard reports that the service is running at record-breaking speeds, while in reality **$100\%$ of customers are experiencing complete checkout failure**.
- **Conclusion**: Minimum time measures the cost of rejection, not the speed of successful work.

---

### 1.2 The Average-Time Fallacy: Blended Meaninglessness in Multimodal Systems
- **The Failure Mode**: Response times in distributed systems do not follow a symmetric bell curve; they are **multimodal** (having multiple distinct peaks).
- **Real-World Scenario**: An e-commerce catalog API handles 100 requests:
  * 95 requests hit a Redis cache and return in $10\text{ms}$.
  * 5 requests miss the cache, block on a PostgreSQL row lock, and take $2,000\text{ms}$ ($2\text{s}$).
  * $$\text{Average} = \frac{(95 \times 10) + (5 \times 2000)}{100} = \frac{950 + 10000}{100} = \mathbf{109.5\text{ms}}$$
- **The Dashboard Illusion**: The dashboard reports an average latency of $109.5\text{ms}$.
  * Did any user wait $109.5\text{ms}$? **No.**
  * $95\%$ of users had a fast $10\text{ms}$ experience.
  * $5\%$ of users (1 out of every 20 customers) suffered an intolerable $2\text{s}$ stall and abandoned their carts.
- **Conclusion**: The average blends success and disaster into a single fictional number that represents nobody's actual experience.

---

### 1.3 The Max-Time Fallacy: Outlier Hysteria vs. Volume Blindness
- **The Failure Mode**: Maximum latency is hypersensitive to a single non-reproducible outlier while remaining completely blind to the blast radius (number of affected users).
- **Real-World Scenario A (False Alarm)**: 1,000,000 mobile users complete checkouts in $50\text{ms}$. One user enters a subterranean parking garage with cell reception dropping packets, causing a TCP socket read timeout of $60\text{ seconds}$. 
  * The Maximum Latency spikes to $60\text{s}$. If an on-call engineer is paged on "Max Latency > 2s", they are woken up at 03:00 AM for a single user's physical antenna drop.
- **Real-World Scenario B (Volume Blindness)**: During a minor database lock contention, 5,000 checkout requests are delayed to $3.9\text{ seconds}$ (violating the $1\text{s}$ SLA). One request takes $4.0\text{ seconds}$.
  * The Max Latency shows $4.0\text{s}$. The metric is incapable of telling leadership whether **1 single customer** was slow or **5,000 customers** were simultaneously impacted.
- **Conclusion**: Max latency measures noise without context; it must always be replaced by high-order percentiles ($p_{99}, p_{99.9}$) correlated with volume counts.

---

### 1.4 The Anatomy of Latency Distributions (PDF vs. CDF)
- **Probability Density Function (PDF)**: Shows the relative frequency of individual latency buckets (the shape of the histogram).
- **Cumulative Distribution Function (CDF)**: Shows the probability that a request completes in $\le X\text{ milliseconds}$. The CDF is monotonic ($0.0 \dots 1.0$), making it the most accurate representation for SLA verification.

---

## 2. Percentiles & Quantiles Explained ($p_{50}$ to $p_{99.99}$)

### 2.1 Mathematical Definition of Quantiles
The $q$-th quantile (or $p$-th percentile where $p = 100q$) of a sorted dataset of $N$ values is the value $V$ such that at least $p\%$ of the observations are less than or equal to $V$:

$$\text{Rank}(p) = \left\lceil \frac{p}{100} \times N \right\rceil$$

### 2.2 The Meaning of the "Nines" in Production Latency

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          PERCENTILE SPECTRUM & MEANING                      │
├────────────┬──────────────────┬─────────────────────────────────────────────┤
│ PERCENTILE │ STATISTICAL NAME │ PRODUCTION MEANING                          │
├────────────┼──────────────────┼─────────────────────────────────────────────┤
│ $p_{50}$   │ Median           │ Typical user experience (half faster/slower)│
│ $p_{90}$   │ 90th Percentile  │ Slowest 1 in 10 requests                    │
│ $p_{95}$   │ 95th Percentile  │ Degradation threshold; ideal for Hedging    │
│ $p_{99}$   │ 99th Percentile  │ Tail latency; slowest 1 in 100 requests     │
│ $p_{99.9}$ │ 3 Nines Latency  │ Severe tail; GC pauses, disk lock stalls    │
│ $p_{99.99}$│ 4 Nines Latency  │ Critical outlier; TCP SYN timeout drops     │
└────────────┴──────────────────┴─────────────────────────────────────────────┘
```

### 2.3 The Fatal Flaw: The Percentile Averaging Fallacy
A pervasive mistake in SRE and DevOps dashboards is computing percentiles per pod and then taking the arithmetic mean across the cluster:

$$\text{FATAL ERROR: } \text{Cluster } p_{99} \ne \frac{p_{99}(\text{Pod } 1) + p_{99}(\text{Pod } 2) + \dots + p_{99}(\text{Pod } K)}{K}$$

**Why is this mathematically invalid?**
- Percentiles are rank-order statistics, not linear scalar values.
- If Pod 1 processes 10,000 requests with $p_{99} = 50\text{ms}$, and Pod 2 processes 100 requests with $p_{99} = 2,000\text{ms}$, averaging them yields $\frac{50 + 2000}{2} = 1,025\text{ms}$. In reality, only 1 request out of 10,100 experienced $2,000\text{ms}$, so the true cluster $p_{99}$ is $\approx 50\text{ms}$.

### 2.4 Correct Cross-Node Percentile Aggregation (HdrHistogram, t-digest & DDSketch)
To calculate true cluster-wide percentiles without centralizing billions of raw raw numbers:
1. **Prometheus Histograms**: Bucketize observations into exponential boundary buckets (`le="0.05"`, `le="0.1"`, `le="0.25"`), then calculate percentiles across pods using `histogram_quantile(0.99, sum(rate(http_duration_bucket[5m])) by (le))`.
2. **t-digest / DDSketch**: Mergeable probabilistic data structures that compress floating-point distributions with guaranteed relative error bounds ($\epsilon \le 1\%$).

---

## 3. Boxplots & Dispersion Analysis (Tukey's Summary)

A Boxplot (Box-and-Whisker Plot) provides a standardized 5-number visual summary of a latency distribution.

```
                  Q1 - 1.5*IQR          Q1      Median (Q2)     Q3          Q3 + 1.5*IQR
                        │                │           │           │                │
                        ▼                ▼           ▼           ▼                ▼
   Outliers (✘)       ──┬────────────────┌───────────┬───────────┐────────────────┬──    Outliers (✘)
    ✘      ✘            │   Left Whisker │  25% - 50%│ 50% - 75% │ Right Whisker  │          ✘   ✘   ✘
  ──────────────────────┼────────────────└───────────┴───────────┘────────────────┼───────────────────
                        │                ▲                       ▲                │
                        │                └───────── IQR ─────────┘                │
                        │                       (Q3 - Q1)                         │
```

### 3.1 The Five-Number Summary & The Interquartile Range (IQR)
1. **Minimum ($Min$)**: The smallest observation within the non-outlier range.
2. **First Quartile ($Q_1$ / 25th Percentile)**: $25\%$ of observations fall below this value.
3. **Median ($Q_2$ / 50th Percentile)**: The midpoint of the distribution.
4. **Third Quartile ($Q_3$ / 75th Percentile)**: $75\%$ of observations fall below this value.
5. **Maximum ($Max$)**: The largest observation within the non-outlier range.

The **Interquartile Range (IQR)** measures statistical dispersion:
$$\text{IQR} = Q_3 - Q_1$$

### 3.2 Outlier Classification ($1.5 \times \text{IQR}$ Rule)
John Tukey established the standard mathematical boundary for detecting anomalous outliers:
- **Mild High Outlier**: $\text{Value} > Q_3 + (1.5 \times \text{IQR})$
- **Extreme High Outlier**: $\text{Value} > Q_3 + (3.0 \times \text{IQR})$

---

## 4. Time Windows & Moving Averages: Sizing the "Last X over Y" Equation

When configuring statistical dashboards, Prometheus queries, and alerting rules, engineering teams must evaluate metrics across two distinct time dimensions:
$$\text{Query Template: } \text{"Metric over the last } \mathbf{X} \text{ (duration) bucketed / compared over } \mathbf{Y} \text{ (resolution/baseline)}"$$

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                     THE "LAST X OVER Y" DUAL-WINDOW MATRIX                  │
├────────────────────┬─────────────────────────────┬──────────────────────────┤
│ PARAMETER          │ DEFINITION                  │ WHAT HAPPENS IF MISSIZED │
├────────────────────┼─────────────────────────────┼──────────────────────────┤
│ $\mathbf{X}$       │ The Evaluation Window       │ • Too small: Flapping and│
│ (Evaluation Range) │ (e.g. "Last 5 minutes",     │   false-alarm alerts.    │
│                    │ "Last 1 hour", "Last 30d"). │ • Too large: High inertia│
│                    │ Determines historical depth.│   hides brief outages.   │
├────────────────────┼─────────────────────────────┼──────────────────────────┤
│ $\mathbf{Y}$       │ The Step / Bucket Resolution│ • Too large: Aliasing /  │
│ (Step / Baseline)  │ (e.g. "every 15 seconds",   │   smoothing hides spikes.│
│                    │ "1-minute buckets", "1w").  │ • Too small: High CPU and│
│                    │ Determines granularity.     │   visual noise.          │
└────────────────────┴─────────────────────────────┴──────────────────────────┘
```

---

### 4.1 Sizing the "Last X over Y" Parameters for Observability Plots

| Operational Task | Optimal $X$ (Evaluation Window) | Optimal $Y$ (Step / Resolution) | Rationale & Practical Example |
| :--- | :--- | :--- | :--- |
| **Real-Time Incident Alerting** | $\mathbf{X = 1\text{m}–5\text{m}}$ | $\mathbf{Y = 15\text{s}}$ | Catches production outages within 60s without waking on-call engineers for a 2-second blip. |
| **Micro-Jitter & GC Stalls** | $\mathbf{X = 30\text{s}–60\text{s}}$ | $\mathbf{Y = 100\text{ms}–500\text{ms}}$ | Resolves individual 200ms Stop-The-World GC pauses or lock contention spikes. |
| **Deployment Canary Verification**| $\mathbf{X = 15\text{m}–30\text{m}}$ | $\mathbf{Y = 1\text{m}}$ | Sufficient statistical sample to compare Canary vs Baseline error rate. |
| **Seasonal Anomaly Detection** | $\mathbf{X = 10\text{m}}$ | $\mathbf{Y = 7\text{ days offset}}$ | Evaluates current 10m order rate against the exact matching 10m window from last week. |
| **Capacity & Trend Planning** | $\mathbf{X = 30\text{d}–90\text{d}}$ | $\mathbf{Y = 1\text{ hour}–1\text{ day}}$ | Filters out intra-day noise to expose month-over-month infrastructure growth curves. |

---

### 4.2 The 3 Critical Sizing Pitfalls (Aliasing, Flapping & Inertia)

#### 1. The Aliasing / Smoothing Blindspot ($Y$ Step Too Large):
- **The Pitfall**: Plotting a 24-hour graph with $Y = 1\text{ hour}$ averaging buckets.
- **Consequence**: If a downstream payment gateway suffered a **complete 10-minute total outage** ($100\%$ errors) at 14:15, averaging that data over a 1-hour bucket reduces the measured failure rate to $\approx 16.6\%$. The graph shows a mild bump, completely masking a SEV-1 outage!

#### 2. The Flapping Alert Trap ($X$ Window Too Small):
- **The Pitfall**: Configuring Prometheus alerts with `rate(http_errors[5s]) > 0.05`.
- **Consequence**: A single dropped network packet causes the alert to fire, and 5 seconds later it automatically resolves. The on-call engineer is inundated with 400 pager notifications per night for normal network jitter.

#### 3. The Inertia Delay Trap ($X$ Window Too Large):
- **The Pitfall**: Configuring incident alerts with `rate(http_errors[1h]) > 0.05`.
- **Consequence**: A 10-minute outage triggers the alert. Engineers deploy an immediate hotfix in 2 minutes. However, because the rolling evaluation window $X$ is 60 minutes long, the alert **continues to fire for 50 minutes after the system is 100% healthy**, preventing verification of recovery.

---

### 4.3 Fixed Windows vs. Sliding Continuous Windows
- **Fixed Window (Tumbling Window)**: Evaluates counts within non-overlapping intervals (e.g. 00:00–00:05, 00:05–00:10). Vulnerable to boundary spikes (a burst split across 00:04:59 and 00:05:01 is halved).
- **Sliding Continuous Window**: Evaluates metrics over the continuous trailing duration $[t - W, t]$.

### 4.2 Simple Moving Average (SMA) vs. Cumulative Moving Average (CMA)
$$\text{SMA}_k = \frac{1}{k} \sum_{i=0}^{k-1} x_{t-i}$$
SMA assigns equal weight to data from 5 minutes ago and data from 1 second ago.

### 4.3 Exponential Weighted Moving Average (EWMA)
EWMA applies exponentially decreasing weights to older observations, making it highly responsive to sudden shifts:

$$S_t = \alpha \cdot x_t + (1 - \alpha) \cdot S_{t-1}$$
Where:
- $S_t$ = Current smoothed value.
- $x_t$ = New raw measurement at time $t$.
- $\alpha$ = Smoothing factor ($0 < \alpha \le 1$). Higher $\alpha$ reacts faster; lower $\alpha$ provides smoother dampening.

---

## 5. Seasonality, Trends & Relative Anomaly Detection

### 5.1 Time-Series Decomposition: Trend, Seasonality & Residuals
Any production metric $Y(t)$ (traffic volume, order rate, active connections) can be decomposed into three components:
$$Y(t) = \text{Trend}(t) + \text{Seasonality}(t) + \text{Residuals}(t)$$

```
RAW METRIC (Y)        =   TREND (Long-Term Growth)  +  SEASONALITY (Weekly Cycle)  +  RESIDUALS (Noise/Incidents)
 ──/\_/\_/\_/\──────>     ──────────────/─────────>    ─/\_/\_/\_/\_/\─────────>      ──_--__--_--───────>
```

### 5.2 Relative Anomaly Detection: X% Deviation from Previous Y Periods
Static alerting thresholds (e.g. `traffic < 500 RPS`) fail because 200 RPS is an outage at 14:00 on Tuesday, but perfectly normal at 04:00 on Sunday.

**Relative Measurement Formula**:
$$\text{Deviation}_{\%} = \frac{Y(t) - Y_{\text{baseline}}(t)}{Y_{\text{baseline}}(t)} \times 100\%$$
Where $Y_{\text{baseline}}(t)$ is the metric measured at the **exact same hour and day of the previous week(s)**:
$$Y_{\text{baseline}}(t) = Y(t - 7\text{ days})$$

### 5.3 Seasonally-Aware Prometheus Alerting (The `offset` Pattern)

```yaml
groups:
  - name: relative_anomaly_alerts
    rules:
      - alert: OrderDropRelativeSeasonality
        # Alert if current order rate is 30% lower than the same 10-minute window last week
        expr: |
          (
            sum(rate(checkout_orders_total[10m]))
            -
            sum(rate(checkout_orders_total[10m] offset 1w))
          )
          /
          sum(rate(checkout_orders_total[10m] offset 1w))
          < -0.30
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "Order velocity dropped >30% compared to same time last week"
          description: "Current RPS: {{ $values.B }}. Baseline last week: {{ $values.C }}."
```

### 5.4 Holt-Winters Triple Exponential Smoothing
Holt-Winters algorithm models three dynamic equations simultaneously:
1. **Level Equation ($\ell_t$)**: The baseline value after adjusting for seasonality.
2. **Trend Equation ($b_t$)**: The expected linear rate of change.
3. **Seasonal Equation ($s_t$)**: The cyclical periodic factor.

$$\hat{y}_{t+h|t} = \ell_t + h \cdot b_t + s_{t+h-m}$$
Used in Prometheus via `predict_linear()` and automated alerting engines (e.g. Datadog Anomaly Detection, AWS CloudWatch Anomaly Detector) to generate dynamic upper and lower confidence bounds around operational metrics.
