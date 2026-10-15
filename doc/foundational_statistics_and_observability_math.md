# Foundational Statistics, Observability Mathematics & Time-Series Analysis

This document provides a comprehensive, beginner-friendly, and mathematically rigorous guide to understanding **Time Series**, **Stochastic Processes**, **Latency Distributions**, **Percentiles**, **Boxplots**, **Moving Averages**, **Seasonality Modeling**, and **Relative Anomaly Detection** for software resilience, QA testing, and telemetry architectures.

> 📊 **Interactive Visual Gallery**: All statistical distributions, outlier models, and smoothing algorithms in this guide are available as standalone interactive Chart.js templates under [`doc/charts/`](./charts/index.html).

---

## Table of Contents

- [1. What is a Time Series? The Stochastic Reality of Computing](#1-what-is-a-time-series-the-stochastic-reality-of-computing)
  - [1.1 The Deterministic Illusion vs. Stochastic Reality](#11-the-deterministic-illusion-vs-stochastic-reality)
  - [1.2 Why Software Execution is Inherently Random](#12-why-software-execution-is-inherently-random)
  - [1.3 What is a "Tail"? (The Grocery Store Checkout Line Analogy)](#13-what-is-a-tail-the-grocery-store-checkout-line-analogy)
  - [1.4 The Three Natural Forces: Intuitive Stochastic Decomposition](#14-the-three-natural-forces-intuitive-stochastic-decomposition)
- [2. The Mathematical Failure of Naive Metrics (Min, Average, Max)](#2-the-mathematical-failure-of-naive-metrics-min-average-max)
  - [2.1 The Min-Time Fallacy: How Fast Errors Masquerade as Peak Performance](#21-the-min-time-fallacy-how-fast-errors-masquerade-as-peak-performance)
  - [2.2 The Average-Time Fallacy: Blended Meaninglessness in Multimodal Systems](#22-the-average-time-fallacy-blended-meaninglessness-in-multimodal-systems)
  - [2.3 The Max-Time Fallacy: Outlier Hysteria vs. Volume Blindness](#23-the-max-time-fallacy-outlier-hysteria-vs-volume-blindness)
  - [2.4 The Anatomy of Latency Distributions: PDF vs. CDF](#24-the-anatomy-of-latency-distributions-pdf-vs-cdf)
  - [2.5 Geometric and Harmonic Means: Why They Still Fail for Latency SLAs](#25-geometric-and-harmonic-means-why-they-still-fail-for-latency-slas)
- [3. Percentiles & Quantiles Explained (p50 to p99.99)](#3-percentiles--quantiles-explained-p50-to-p9999)
  - [3.1 Plain-English Definition of Percentiles](#31-plain-english-definition-of-percentiles)
  - [3.2 The Architectural Anatomy of the "Nines" (p50 to p99.99)](#32-the-architectural-anatomy-of-the-nines-p50-to-p9999)
  - [3.3 The Fatal Flaw: The Percentile Averaging Fallacy (Jensen's Inequality)](#33-the-fatal-flaw-the-percentile-averaging-fallacy-jensens-inequality)
  - [3.4 Mergeable Quantile Sketches: t-digest, HdrHistogram & DDSketch](#34-mergeable-quantile-sketches-t-digest-hdrhistogram--ddsketch)
  - [3.5 Prometheus Histogram Bucket Interpolation (`histogram_quantile`)](#35-prometheus-histogram-bucket-interpolation-histogram_quantile)
- [4. Boxplots & Dispersion Analysis (Tukey's Summary)](#4-boxplots--dispersion-analysis-tukeys-summary)
  - [4.1 Tukey's Five-Number Summary & The Interquartile Range (IQR)](#41-tukeys-five-number-summary--the-interquartile-range-iqr)
  - [4.2 Standard Deviation (Sigma) vs. IQR: The Failure of Gaussian Normality](#42-standard-deviation-sigma-vs-iqr-the-failure-of-gaussian-normality)
  - [4.3 Mathematical Outlier Classification (1.5 * IQR and 3.0 * IQR Rules)](#43-mathematical-outlier-classification-15--iqr-and-30--iqr-rules)
  - [4.4 Skewness & Kurtosis in Operational Telemetry](#44-skewness--kurtosis-in-operational-telemetry)
- [5. Time Windows & Moving Averages: Sizing the "Last X over Y" Equation](#5-time-windows--moving-averages-sizing-the-last-x-over-y-equation)
  - [5.1 The "Last X over Y" Dual-Window Framework](#51-the-last-x-over-y-dual-window-framework)
  - [5.2 Comprehensive Sizing Matrix for Operational Tasks](#52-comprehensive-sizing-matrix-for-operational-tasks)
  - [5.3 The 3 Critical Sizing Pitfalls (Aliasing, Flapping & Inertia)](#53-the-3-critical-sizing-pitfalls-aliasing-flapping--inertia)
  - [5.4 Fixed (Tumbling) Windows vs. Sliding Continuous Windows](#54-fixed-tumbling-windows-vs-sliding-continuous-windows)
  - [5.5 Simple Moving Average (SMA) vs. Linear Weighted Moving Average (WMA)](#55-simple-moving-average-sma-vs-linear-weighted-moving-average-wma)
  - [5.6 Exponential Weighted Moving Average (EWMA) & Half-Life Physics](#56-exponential-weighted-moving-average-ewma--half-life-physics)
- [6. Seasonality, Trends & Relative Anomaly Detection](#6-seasonality-trends--relative-anomaly-detection)
  - [6.1 Time-Series Decomposition: Additive vs. Multiplicative Models](#61-time-series-decomposition-additive-vs-multiplicative-models)
  - [6.2 Relative Anomaly Detection: Measuring Percentage Deviations](#62-relative-anomaly-detection-measuring-percentage-deviations)
  - [6.3 Seasonally-Aware Prometheus Alerting via the `offset` Pattern](#63-seasonally-aware-prometheus-alerting-via-the-offset-pattern)
  - [6.4 Holt-Winters Triple Exponential Smoothing (Level, Trend & Seasonality)](#64-holt-winters-triple-exponential-smoothing-level-trend--seasonality)

---

## 1. What is a Time Series? The Stochastic Reality of Computing

### 1.1 The Deterministic Illusion vs. Stochastic Reality

When junior software engineers write a function, they typically think deterministically:
> *"My function executes 5 instructions and performs 1 SQL query. Therefore, it will always take exactly 5 milliseconds."*

In the real world of distributed computing and multi-tenant cloud hardware, this assumption is completely false. A piece of software is not a deterministic mechanical clockwork; it is a **Stochastic System**—a system whose state and behavior are governed by probability, randomness, and statistical entropy over time.

A **Time Series** is simply a sequence of observations of a variable recorded at successive, discrete points in time:

```math
X(t_1), X(t_2), X(t_3), \dots, X(t_N)
```

In production systems, our telemetry creates dozens of parallel time series:
- Request Arrival Rates (Requests Per Second at timestamp *t*)
- Execution Latencies (Milliseconds elapsed for request *i* at timestamp *t*)
- Error Rates (Percentage of failed requests at timestamp *t*)
- Resource Consumption (CPU usage, open sockets, memory allocation at timestamp *t*)

```
TIME SERIES TRACE: Request Latency Sampled Every Second
Latency (ms)
 120ms │                                       ▲ (Outage Spike)
 100ms │                                      █│█
  80ms │                                      █│█
  60ms │                     ▲ (GC Pause)     █│█
  40ms │     ▲              ███               ███
  20ms │─█──███──██──█──────████───█──██───█──████──█───█─── (Baseline Jitter)
   0ms └─┴────┴───┴──┴───────┴─────┴───┴───┴────┴───┴───┴───> Time (Seconds)
       00:01 00:02 00:03   00:04 00:05 00:06  00:07 00:08
```

---

### 1.2 Why Software Execution is Inherently Random

Why does the exact same line of Go code take 2ms on one execution and 450ms on the very next? 

Five physical and environmental factors introduce continuous randomness into every software execution:

1. **Hardware & Operating System Scheduling (Noisy Neighbors)**:
   The Linux kernel continuously context-switches CPU cores between thousands of background threads. If an OS thread is preempted while holding a lock or waiting for memory, execution pauses randomly.
2. **Memory Allocation & Garbage Collection (GC)**:
   Languages with automated memory management (Go, Java) periodically initiate garbage collection scans. During a Stop-The-World (STW) phase or background mark-assist, goroutines pause for 1ms to 50ms.
3. **Physical Network Physics & Jitter**:
   Packets traverse physical switches, fiber cables, and wireless cellular radios. Transient packet drops force TCP retransmissions, adding an unavoidable 200ms to 1000ms step penalty.
4. **Shared Logical Contention (Database Row Locks)**:
   If 50 pods attempt to deduct inventory from the same hot item row simultaneously, 49 pods must wait in an OS queue. The 50th pod finishes in 1ms, while the 1st pod waits 150ms.
5. **Variable Human Payloads**:
   One user fetches a shopping cart with 1 item (2KB JSON payload); the next user fetches a bulk corporate order with 10,000 items (15MB JSON payload requiring 80ms to serialize).

---

### 1.3 The Probability Function P, Random Variables & Core Mathematical Laws

Before analyzing latencies, error distributions, or system tails, we must understand the foundational language of probability used across software telemetry.

#### 1. The Probability Function P(E) & Its Three Fundamental Axioms
The **Probability Function**, denoted by **P(E)**, assigns a real number between 0 and 1 to an event **E** occurring within a system:

```math
0 \le P(E) \le 1
```

- **P(E) = 0**: The event is mathematically impossible (e.g. an HTTP response completing in negative milliseconds).
- **P(E) = 1**: The event is absolutely guaranteed (e.g. an executed process eventually either succeeds, fails, or times out).
- **P(Ω) = 1**: The sum of probabilities across the entire sample space **Ω** (all possible outcomes combined) always equals exactly 1 (100%).

---

#### 2. Random Variables: Capital X (The Process) vs. Lowercase x (The Value)
In mathematical statistics, notation distinguishes between the system process itself and the specific numerical value it takes:

- **Capital Letter (X, Y, L)** represents a **Random Variable**—the underlying non-deterministic process or measurement whose value varies over time.
  * *Example*: Let **L** be the random variable representing the *"End-to-end latency of the checkout API"*.
- **Lowercase Letter (x, y, k)** represents a **Specific Realized Value** or **Threshold boundary**.
  * *Example*: Let **k = 200ms** represent our SLA latency limit.

---

#### 3. Understanding Standard Probability Expressions in Telemetry

| Mathematical Expression | Formal Meaning in English | Real-World Operational Example |
| :--- | :--- | :--- |
| **P(X = x)** | **Point Probability**: The exact probability that random variable *X* equals the specific value *x*. | `P(HTTP_Status = 500) = 0.01` (1% chance of returning status 500). For continuous latency down to the nanosecond, point probability is near zero. |
| **P(X ≤ k)** | **Cumulative Upper-Bound Probability**: The probability that *X* is less than or equal to threshold *k*. | `P(Latency ≤ 200ms) = 0.95` (95% of all requests finish within 200ms; this is our p95 SLA definition). |
| **P(X ≤ 0.9)** | **Normalized Ratio Bound**: The probability that a normalized metric (e.g. CPU load factor or memory fraction) is ≤ 0.90 (90%). | `P(Memory_Utilization ≤ 0.90) = 0.99` (99% of the time, memory remains at or below 90% capacity). |
| **P(X > k)** | **Exceedance / Tail Probability**: The probability that *X* strictly exceeds the threshold *k*. | `P(Latency > 800ms) = 0.05` (5% of requests breach the 800ms threshold). |
| **P(a < X ≤ b)** | **Interval Probability**: The probability that *X* falls within the bounded range between *a* and *b*. | `P(10ms < Latency ≤ 50ms) = 0.80` (80% of requests land in the typical cache-hit range). |

---

#### 4. The Complement Rule: P(not X) = 1 - P(X)
The **Complement Rule** (derived from the Law of Total Probability) states that the probability of an event *NOT* occurring is equal to 1 minus the probability of the event occurring:

```math
P(\text{not } A) = 1 - P(A) \quad \text{also written as} \quad P(A^c) = 1 - P(A)
```

##### Practical Microservice Example:
Suppose a single microservice dependency has an availability SLA of 90% fast responses:
- The probability that a call to this service is fast: `P(Fast) = 0.90` (90%).
- What is the probability that this call is **slow**?

```math
P(\text{Slow}) = P(\text{not Fast}) = 1 - P(\text{Fast}) = 1 - 0.90 = \mathbf{0.10} \quad (10\%)
```

---

#### 5. The Product Rule for Independent Events (Joint Probability)
When two operations **A** and **B** are statistically independent (meaning the outcome of service A has zero physical influence on the outcome of service B), the probability that **BOTH** events occur simultaneously is the product of their individual probabilities:

```math
P(A \cap B) = P(A) \times P(B)
```

For **N independent operations** running in parallel:

```math
P(A_1 \cap A_2 \cap \dots \cap A_N) = \prod_{i=1}^{N} P(A_i)
```

---

### 1.4 What is a "Tail"? The Grocery Store Checkout Line & Multi-Service Fan-Out Physics

In observability and statistics, you will constantly hear the term **"Tail Latency"** or **"The Long Tail"**. What does this mean in plain English?

#### The Grocery Store Checkout Line Analogy:
Imagine walking into a large supermarket with 10 checkout registers:
- 9 registers have customers with 2 items paying by card. Their checkout takes **1 minute** each.
- 1 register has a customer whose barcode is damaged, whose card is declined, and who requests a manual price check. That line takes **30 minutes**.

If you look at the store's customer distribution:
- 90% of shoppers finish in 1 minute or less (The "Fast Body").
- A tiny 10% of shoppers wait 30 minutes (The "Long Tail").

```
SHOPPER COUNT
  │  [ The Fast Body (90% of shoppers: 1 min) ]
  │   ████████████████████
  │   ████████████████████
  │   ████████████████████
  │   ████████████████████
  │                                    [ The Long Tail (10% of shoppers: 30 min) ]
  │                                     █
──┴─────────────────────────────────────█──────────────────────────────> TIME (Minutes)
     1 min                              30 min
```

---

#### The Multi-Service Fan-Out Paradox: Why P(All Fast) = 12.1% and 87.9% of Users Stall

In modern cloud architectures, a single user click does not hit just one server. An ingress API Gateway or web frontend fans out requests to **N backend microservices in parallel** (e.g. Payment, Inventory, Tax, Shipping, Recommendations, Pricing, Fraud, User Profile, Notification, Loyalty...).

The entire user page cannot render until the **slowest downstream service** returns.

##### Step-by-Step Mathematical Derivation:
Suppose each individual microservice has an excellent **90% fast response probability** (`P(Fast) = 0.90`), and a **10% slow tail probability** (`P(Slow) = 0.10`).

If the page makes **N = 20 parallel independent API calls**:

1. **Step 1: Compute P(All Fast)**  
   For the user's page to be completely fast, Service 1 must be fast **AND** Service 2 must be fast **AND** ... **AND** Service 20 must be fast.  
   Applying the **Product Rule for Independent Events**:

```math
P(\text{All 20 Fast}) = P(\text{Fast}_1) \times P(\text{Fast}_2) \times \dots \times P(\text{Fast}_{20}) = (0.90)^{20}
```

```math
(0.90)^{20} = 0.12157665459 \approx \mathbf{12.16\%} \approx \mathbf{12.1\%}
```

2. **Step 2: Apply the Complement Rule P(not X) = 1 - P(X)**  
   What is the probability that the user experiences a **slow, stalled page**?  
   A page is slow if **at least one** of the 20 downstream services falls into the slow tail.  
   By the Complement Rule:

```math
P(\text{At Least One Service Slow}) = P(\text{not All Fast}) = 1 - P(\text{All Fast})
```

```math
P(\text{At Least One Service Slow}) = 1 - (0.90)^{20} = 1 - 0.12157665459 = 0.87842334541 \approx \mathbf{87.84\%} \approx \mathbf{87.9\%}
```

**Result: Even though every single microservice is healthy and fast 90% of the time, 87.9% of your end users will experience a frustrating 30-minute stall!**

---

#### Fan-Out Latency Amplification Table:
Notice how rapidly tail latency destroys overall user experience as the number of parallel microservices (*N*) scales:

| Number of Parallel Microservice Calls (N) | Probability of Individual Call Being Fast P(Fast) | Probability Entire Page is Fast P(All Fast) = (0.90)^N | Probability User Hits Slow Tail P(At least one Slow) = 1 - P(All Fast) | Operational Impact |
| :---: | :---: | :---: | :---: | :--- |
| **N = 1** | 90.0% | **90.0%** | **10.0%** | Baseline single service call. |
| **N = 3** | 90.0% | **72.9%** | **27.1%** | 1 in 4 users experience lag. |
| **N = 5** | 90.0% | **59.0%** | **41.0%** | Over 40% of users experience slow page loads. |
| **N = 10** | 90.0% | **34.9%** | **65.1%** | The majority of users (65%) suffer tail latency. |
| **N = 20** | 90.0% | **12.2%** | **87.8% (≈ 87.9%)** | **Catastrophic UX failure**: Almost 9 out of 10 users stall. |
| **N = 50** | 90.0% | **0.5%** | **99.5%** | Fast page loads are virtually impossible (0.5% chance). |

##### The Architectural Imperative:
This mathematical compounding effect is why resilience engineering exists. You cannot fix multi-service fan-out latency simply by optimizing individual SQL queries. You **must** implement active resilience patterns:
1. **Aggressive Per-Attempt Timeouts**: Cut off the 10% slow tail at 150ms instead of waiting 30 seconds.
2. **Hedge Policies**: Fire a redundant parallel request if the first attempt doesn't return within the p95 latency window.
3. **Graceful Fallbacks & Caches**: Return stale cached catalog data or non-critical empty recommendations when a secondary dependency lags.
4. **Circuit Breakers**: Fast-fail failing downstream dependencies before they consume worker threads.

---

### 1.5 The Three Natural Forces: Intuitive Stochastic Decomposition

When analyzing any metric over days, weeks, or months, the graph looks like a chaotic, jagged mountain range. However, statistics allows us to decompose that chaotic signal into **three distinct natural forces**:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                 THE 3 FORCES OF TIME-SERIES DECOMPOSITION                   │
├────────────────────┬─────────────────────────────┬──────────────────────────┤
│ COMPONENT          │ WHAT IT REPRESENTS          │ REAL-WORLD ANALOGY       │
├────────────────────┼─────────────────────────────┼──────────────────────────┤
│ 1. Trend T(t)      │ The underlying long-term    │ A child growing taller   │
│                    │ directional movement.       │ year over year.          │
├────────────────────┼─────────────────────────────┼──────────────────────────┤
│ 2. Seasonality S(t)│ The repeating, predictable  │ The sun rising and       │
│                    │ cyclical heartbeat.         │ setting every 24 hours.  │
├────────────────────┼─────────────────────────────┼──────────────────────────┤
│ 3. Residual I(t)   │ The unpredictable, sudden   │ A sudden thunderstorm or │
│                    │ anomalies and glitches.     │ traffic accident.        │
└────────────────────┴─────────────────────────────┴──────────────────────────┘
```

1. **Trend (**T(t)**)**:
   Your business is growing by 5% every month. Over a 1-year graph, the baseline traffic drifts steadily upward.
2. **Seasonality (**S(t)**)**:
   Human beings sleep at night and shop during the day. Traffic rises at 08:00 AM, peaks at 14:00 PM, and drops at 03:00 AM every single day. On Black Friday or weekends, traffic surges predictably.
3. **Residual Noise (**I(t)**)**:
   The random spikes, network fiber cuts, database crashes, or memory leaks that occur unexpectedly. Observability alert rules exist solely to isolate **Residual Anomalies** from natural Trend and Seasonality.

---

## 2. The Mathematical Failure of Naive Metrics (Min, Average, Max)

In production software operations, engineering teams frequently rely on three naive summary metrics: **Minimum Latency**, **Average Latency**, and **Maximum Latency**. In real-world distributed architectures, each of these metrics fails to represent system health and actively misleads operational decision-making.

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

### 2.1 The Min-Time Fallacy: How Fast Errors Masquerade as Peak Performance
- **The Failure Mode**: The fastest response a web server can possibly generate is an **immediate failure or rejection**.
- **Real-World Production Scenario**: 
  When an upstream authentication gateway expires, or a backend PostgreSQL cluster runs out of connections and crashes, the Go HTTP handler encounters an immediate connection failure. It returns an HTTP `500 Internal Server Error` or `503 Service Unavailable` in 0.2ms without parsing JSON request payloads, checking caches, or executing business transactions.
- **The Dashboard Illusion**: 
  The "Minimum Response Time" metric drops from a healthy 15ms baseline down to 0.2ms. A naive monitoring dashboard or automated auto-scaling check reports that the service is running at record-breaking efficiency, while in reality **100% of user transactions are failing**.
- **HTTP 304 Not Modified Trap**: 
  Minimum latency also captures static conditional requests (HTTP 304) where zero compute occurs, skewing performance baselines without reflecting true computational work.
- **Architectural Conclusion**: Minimum time measures the computational cost of rejection or cache bypassing; it tells you nothing about the health of active application processing.

---

### 2.2 The Average-Time Fallacy: Blended Meaninglessness in Multimodal Systems
- **The Failure Mode**: Response times in distributed systems do not follow a Gaussian (Normal / Bell-curve) distribution; they are **multimodal** (exhibiting multiple distinct distribution peaks).
- **Real-World Production Scenario**: 
  An e-commerce catalog API handles 100 requests:
  * 95 requests hit a Redis cache and return in 10ms.
  * 5 requests miss the cache, block on a PostgreSQL row lock (`SELECT ... FOR UPDATE`), and take 2,000ms (2 seconds).

```math
\text{Arithmetic Mean } (\mu) = \frac{(95 \times 10) + (5 \times 2000)}{100} = \frac{950 + 10000}{100} = \mathbf{109.5\text{ ms}}
```

- **The Dashboard Illusion**: 
  The dashboard reports an average latency of 109.5ms.
  * Did any user wait 109.5ms? **No.**
  * 95% of users had a fast 10ms experience.
  * 5% of users (1 out of every 20 customers) suffered an intolerable 2-second stall and abandoned their shopping carts.
- **The Asymmetry of User Frustration**: 
  A fast response creates mild user satisfaction, but an extreme delay causes immediate user departure. An arithmetic mean treats 10 users waiting 100ms as mathematically equivalent to 1 user waiting 1,000ms, but in human economics, the 1-second stall permanently loses a customer.
- **Architectural Conclusion**: The arithmetic average blends success and disaster into a single fictional number that represents nobody's actual experience.

```
REQUEST COUNT (PDF)
  │  [ Peak 1: Fast Cache Path (10ms) ]
  │   ██████████████████
  │   ██████████████████ (95% of users)
  │   ██████████████████
  │   ██████████████████
  │                                    [ Peak 2: DB Row Lock Stall (2000ms) ]
  │                                     █ (5% of users - SLA Violated!)
──┴─────────────────────────────────────█──────────────────────────────> LATENCY
     0ms                                2000ms
               ▲
               └── Arithmetic Mean (109.5ms) represents an EMPTY VALLEY where zero requests exist!
```

---

### 2.3 The Max-Time Fallacy: Outlier Hysteria vs. Volume Blindness
- **The Failure Mode**: Maximum latency is hypersensitive to a single non-reproducible outlier while remaining completely blind to the blast radius (number of affected users).
- **Real-World Scenario A (Outlier Hysteria / False Alarm)**: 
  A high-scale API processes 1,000,000 requests in 50ms. One user enters a subway tunnel on mobile cell reception with high packet loss, causing a TCP socket read timeout of 60 seconds. 
  * The Maximum Latency spikes to 60s. If an on-call engineer is paged on "Max Latency > 2s", they are woken up at 03:00 AM for a single user's physical radio antenna disconnect.
- **Real-World Scenario B (Volume Blindness / Masked Outage)**: 
  During a database lock escalation incident, 5,000 checkout requests are delayed to 3.9 seconds (violating the 1s SLA). One request takes 4.0 seconds.
  * The Max Latency shows 4.0s. The metric is incapable of telling engineering leadership whether **1 single customer** was slow or **5,000 customers** were simultaneously failing.
- **Architectural Conclusion**: Max latency measures noise without context; it must always be replaced by high-order percentiles (p99, p99.9) correlated with volume counters.

---

### 2.4 The Anatomy of Latency Distributions: PDF vs. CDF

In software observability, response times are represented by two mathematical functions:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                       PDF vs. CDF IN LATENCY MODELING                       │
├──────────────────────────────────────┬──────────────────────────────────────┤
│ 1. Probability Density Function (PDF)│ 2. Cumulative Distribution Function  │
│    (Histogram / Frequency Curve)     │    (Monotonic SLA Compliance Curve)  │
├──────────────────────────────────────┼──────────────────────────────────────┤
│ Shows the relative frequency of      │ Shows the exact probability that a   │
│ individual response time buckets.    │ request finishes in <= X ms.         │
│ Identifies multimodal clusters.      │ Range: Strictly 0.0 (0%) to 1.0 (100)│
└──────────────────────────────────────┴──────────────────────────────────────┘
```math
#### 1. Probability Density Function (PDF):
```
f(x) = P(X = x)
```math
The PDF visualizes where execution time clusters. A service with an in-memory cache, an edge CDN, and a database will exhibit a **trimodal PDF** with distinct peaks at 1ms (RAM), 25ms (CDN), and 150ms (DB).

#### 2. Cumulative Distribution Function (CDF):
```
F(x) = P(X \le x) = \int_{0}^{x} f(u) \, du
```math
The CDF is strictly monotonically increasing (0 <= F(x) <= 1). 
- To verify an SLA contract (*"99% of requests must complete in 500ms or less"*), evaluate the CDF at x = 500ms. If F(500ms) >= 0.99, the SLA is satisfied.

---

### 2.5 Geometric and Harmonic Means: Why They Still Fail for Latency SLAs

To overcome the arithmetic mean's distortion by large outliers, statisticians sometimes propose alternative Pythagorean means:

1. **Geometric Mean**:
```
G = \left(\prod_{i=1}^{N} x_i\right)^{\frac{1}{N}} = \exp\left(\frac{1}{N} \sum_{i=1}^{N} \ln x_i\right)
```math
*Use Case*: Useful for normalizing benchmarks across different machines (e.g., SPEC CPU benchmarks) where relative ratios matter.
   *Failure in Latency*: Severely dampens extreme tails (a 2,000ms stall is log-compressed to ln(2000) ≈ 7.6), masking severe user latency spikes.

2. **Harmonic Mean**:
```
H = \frac{N}{\sum_{i=1}^{N} \frac{1}{x_i}}
```math
*Use Case*: Useful for averaging rates (e.g. Transactions Per Second / throughput across nodes).
   *Failure in Latency*: Heavily biased toward the smallest numbers. A single fast failure (0.2ms) pulls the entire harmonic mean down to near-zero (1 / 0.2 = 5), making an outage look like high-speed performance.

**Conclusion**: No single parametric scalar mean (Arithmetic, Geometric, or Harmonic) can characterize distributed system latency. Only **non-parametric quantiles (percentiles)** provide honest, actionable visibility.

---

## 3. Percentiles & Quantiles Explained (p50 to p99.99)

### 3.1 Plain-English Definition of Percentiles

A **Percentile** is the score below which a given percentage of observations falls.
- If your service's 95th Percentile (p95) is **120ms**, it means:
  * *"95 out of every 100 requests finished in 120ms or faster."*
  * *"Only 5 out of every 100 requests were slower than 120ms."*

#### Mathematical Estimation:
For an ordered set of N latency measurements x(1) <= x(2) <= ... <= x(N):
```
\text{Rank}(p) = \left\lceil \frac{p}{100} \times N \right\rceil
```
```math
p\text{-th Percentile} = x_{(\text{Rank})}
```

For N = 1,000 requests:
- p50 (Median) is the 500-th sorted value.
- p95 is the 950-th sorted value.
- p99 is the 990-th sorted value.
- p99.9 is the 999-th sorted value.

---

### 3.2 The Architectural Anatomy of the "Nines" (p50 to p99.99)

In production systems, each percentile band maps to a specific physical layer of the infrastructure:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      WHAT EACH PERCENTILE BAND MEASURES                     │
├────────────┬─────────────────────────────┬──────────────────────────────────┤
│ PERCENTILE │ PHYSICAL / ARCHITECTURAL    │ WHAT SYSTEM EVENTS OCCUR HERE    │
│            │ DOMAIN                      │                                  │
├────────────┼─────────────────────────────┼──────────────────────────────────┤
│ p50        │ The Core Fast Path          │ Cache hits, optimized SQL queries│
│ (Median)   │                             │ with active connection reuse.    │
├────────────┼─────────────────────────────┼──────────────────────────────────┤
│ p90        │ Minor Resource Competition  │ CPU scheduling delays, minor OS  │
│            │                             │ thread preemption, TLS handshakes│
├────────────┼─────────────────────────────┼──────────────────────────────────┤
│ p95        │ Degradation Threshold       │ Database disk reads (cache miss),│
│            │ (The Hedging Boundary)      │ single network packet re-orders. │
├────────────┼─────────────────────────────┼──────────────────────────────────┤
│ p99        │ Severe Contention Tail      │ Lock waits, retries with backoff,│
│            │                             │ Linux CFS CPU quota throttling.  │
├────────────┼─────────────────────────────┼──────────────────────────────────┤
│ p99.9      │ Infrastructure Glitches     │ Go Stop-The-World GC pauses,     │
│ (3 Nines)  │                             │ VM noisy-neighbor CPU steals.    │
├────────────┼─────────────────────────────┼──────────────────────────────────┤
│ p99.99     │ Kernel / Physical Network   │ TCP SYN retransmit drops (1s/3s),│
│ (4 Nines)  │ Dropouts                    │ Pod rescheduling / OOM restarts. │
└────────────┴─────────────────────────────┴──────────────────────────────────┘
```math
---

### 3.3 The Fatal Flaw: The Percentile Averaging Fallacy (Jensen's Inequality)

A widespread, catastrophic error in SRE and DevOps dashboards is computing p99 per pod and then taking the arithmetic average of those p99 values across the cluster:
```
\text{FATAL ERROR: } \text{Cluster } p_{99} \ne \frac{p_{99}(\text{Pod } 1) + p_{99}(\text{Pod } 2) + \dots + p_{99}(\text{Pod } K)}{K}
```math
#### Mathematical Proof of the Fallacy:
Percentiles are **rank-order non-linear operators**, not linear summations. By Jensen's Inequality, for any non-linear transformation g(X):
```
E[g(X)] \ne g(E[X])
```math
#### Real-World Production Example:
Consider a cluster of 2 Kubernetes pods:
- **Pod 1 (High Traffic, Healthy)**: Processes 10,000 requests with p99 = 50ms (only 100 requests were slower than 50ms).
- **Pod 2 (Low Traffic / Startup, Degraded)**: Processes 100 requests with p99 = 2,000ms (only 1 request took 2000ms).
```
\text{Naive Arithmetic Average of } p_{99} = \frac{50\text{ ms} + 2000\text{ ms}}{2} = \mathbf{1,025\text{ ms}}
```

**What is the TRUE Cluster p99?**
- Total cluster requests = 10,000 + 100 = 10,100 requests.
- The 99th percentile represents the 99% mark, which leaves the slowest 1% of total traffic (101 requests).
- Pod 1 had 100 slow requests, and Pod 2 had 1 slow request (100 + 1 = 101 slow requests).
- The true 99th percentile across all 10,100 requests is **≈ 50ms**!
- The naive average reported 1,025ms (20x higher than reality), triggering false panic. Conversely, if Pod 1 had been slow and Pod 2 fast, the average would have completely masked a major outage.

---

### 3.4 Mergeable Quantile Sketches: t-digest, HdrHistogram & DDSketch

To compute accurate cluster-wide percentiles across hundreds of nodes without centralizing billions of raw integers, distributed telemetry engines use **mergeable probabilistic data sketches**:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                 MERGEABLE QUANTILE DATA SKETCH COMPARISON                   │
├────────────────────┬─────────────────────────────┬──────────────────────────┤
│ SKETCH ALGORITHM   │ CORE MECHANISM              │ BEST OPERATIONAL USE     │
├────────────────────┼─────────────────────────────┼──────────────────────────┤
│ 1. t-digest        │ Dynamic 1D centroid         │ Extreme tail accuracy    │
│    (Ted Dunning)   │ clustering with non-linear  │ (p99.9, p99.99)          │
│                    │ scale function delta(q).    │ in OpenSearch/Elastic.   │
├────────────────────┼─────────────────────────────┼──────────────────────────┤
│ 2. HdrHistogram    │ Logarithmic integer array   │ Sub-microsecond recording│
│    (Gil Tene)      │ with fixed precision and    │ in low-latency Go/Java   │
│                    │ zero runtime allocations.   │ services; fixes CO.      │
├────────────────────┼─────────────────────────────┼──────────────────────────┤
│ 3. DDSketch        │ Logarithmic bucket sizing   │ Guaranteed relative error│
│    (Datadog)       │ with strict relative error  │ bounds (<= 1%) in        │
│                    │ bound epsilon.              │ streaming metrics.       │
└────────────────────┴─────────────────────────────┴──────────────────────────┘
```

#### 1. t-digest Mechanics:
- Clusters data into a sequence of K centroids `(C_i = (μ_i, w_i))`.
- Uses a non-linear scale function:

```math
\delta(q) = \frac{\arcsin(2q - 1)}{\pi}
```

  This forces centroids near q = 0 (min) and q = 1 (tail) to be extremely small (high resolution), while allowing larger centroids in the median (q = 0.5).
- **Mergeability**: Two t-digests from different Kubernetes pods can be merged in `O(K)` time simply by concatenating and sorting centroids.

#### 2. HdrHistogram & Coordinated Omission:
- Covers dynamic ranges from 1 microsecond to 1 hour with configurable significant value digits (e.g., 3 digits).
- **Coordinated Omission Correction**: If a client sends requests every 10ms, but a server stall freezes the connection for 1,000ms, standard benchmarks record only **1 slow request** (omitting the 99 requests that were prevented from sending). HdrHistogram automatically injects the missing coordinated omissions to reflect true user experience.

---

### 3.5 Prometheus Histogram Bucket Interpolation (`histogram_quantile`)

Prometheus calculates percentiles on the server side using cumulative bucket counters (`_bucket{le="..."}`):

```math
\text{histogram\_quantile}\left(0.99, \sum_{i} \text{rate}(\text{http\_request\_duration\_seconds\_bucket}[5\text{m}]) \text{ by } (\text{le})\right)
```

#### How Prometheus Computes Percentiles Without Raw Data:
1. Sums the counter rates for each exponential bucket across all selected pods.
2. Identifies the specific bucket `[le_lower, le_upper]` in which the target quantile rank falls.
3. Performs **linear interpolation** within that bucket:

```math
Q(q) = le_{lower} + (le_{upper} - le_{lower}) \times \frac{q \cdot N_{total} - \text{Count}_{lower}}{\text{Count}_{upper} - \text{Count}_{lower}}
```

**Crucial Sizing Rule**: Choose histogram bucket boundaries (`le`) that tightly bracket your SLA threshold (e.g., `le="0.1", le="0.25", le="0.5", le="0.8", le="1.0"` for an 800ms SLA). If all tail requests land in the `+Inf` bucket, `histogram_quantile` returns mathematically meaningless interpolations.

---

## 4. Boxplots & Dispersion Analysis (Tukey's Summary)

A Boxplot (Box-and-Whisker Plot), formulated by mathematician John Tukey in 1977, provides a standardized 5-number visual summary of a latency distribution.

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
```math
---

### 4.1 Tukey's Five-Number Summary & The Interquartile Range (IQR)

1. **Minimum (Min)**: The smallest data point within the lower fence (Min >= Q1 - 1.5 * IQR).
2. **First Quartile (Q1 / 25th Percentile)**: The value below which 25% of observations lie.
3. **Median (Q2 / 50th Percentile)**: The geometric midpoint of the distribution.
4. **Third Quartile (Q3 / 75th Percentile)**: The value below which 75% of observations lie.
5. **Maximum (Max)**: The largest data point within the upper fence (Max <= Q3 + 1.5 * IQR).

The **Interquartile Range (IQR)** represents the middle 50% of the entire dataset:
```
\text{IQR} = Q_3 - Q_1
```math
---

### 4.2 Standard Deviation (Sigma) vs. IQR: The Failure of Gaussian Normality

In classical introductory statistics, dispersion is measured via Sample Variance (s²) and Standard Deviation (σ):
```
\sigma = \sqrt{\frac{1}{N-1} \sum_{i=1}^{N} (x_i - \mu)^2}
```math
#### Why Standard Deviation Fails for Service Latency:
- The standard deviation formula relies on squaring differences from the mean (x_i - μ)².
- In a Gaussian distribution, the Empirical 68-95-99.7 Rule holds (μ ± 2σ covers 95.4%, μ ± 3σ covers 99.7%).
- **In Latency Data**: Because latency distributions have heavy right-side tails, squaring a 5-second timeout inflates σ exponentially. Furthermore, applying μ - 2σ often yields a **negative number of milliseconds** (e.g. -140ms), which is physically impossible.
- **Resilience Rule**: Always use **IQR and Percentiles** (Non-Parametric statistics) rather than Standard Deviation (Parametric statistics) to measure latency dispersion.

---

### 4.3 Mathematical Outlier Classification (1.5 * IQR and 3.0 * IQR Rules)

John Tukey defined rigorous mathematical boundaries ("fences") to classify anomalous observations:
```
\text{Lower Inner Fence} = Q_1 - (1.5 \times \text{IQR})
```
```math
\text{Upper Inner Fence} = Q_3 + (1.5 \times \text{IQR})
```
```math
\text{Lower Outer Fence} = Q_1 - (3.0 \times \text{IQR})
```
```math
\text{Upper Outer Fence} = Q_3 + (3.0 \times \text{IQR})
```math
- **Mild High Outlier**: Any latency observation where:
  Q3 + (1.5 * IQR) < Latency <= Q3 + (3.0 * IQR)
- **Extreme High Outlier**: Any latency observation where:
  Latency > Q3 + (3.0 * IQR)

*Operational Application*: When building automated load-test assertions, flag any test run where the ratio of Extreme High Outliers exceeds 0.1% of total traffic.

---

### 4.4 Skewness & Kurtosis in Operational Telemetry

Two higher-order statistical moments provide immediate diagnosis of system behavior:

#### 1. Skewness (γ1 / Asymmetry):
```
\gamma_1 = \frac{\frac{1}{N} \sum_{i=1}^{N} (x_i - \mu)^3}{\sigma^3}
```math
- γ1 = 0: Perfectly symmetric distribution.
- γ1 > 0: **Positive (Right) Skew**. Normal for healthy software systems (vast majority of traffic fast, long tail of slow queries).
- Sudden spike in γ1: Indicates tail latency degradation while the median remains unchanged.

#### 2. Kurtosis (γ2 / Heavy-Tailedness):
```
\gamma_2 = \frac{\frac{1}{N} \sum_{i=1}^{N} (x_i - \mu)^4}{\sigma^4} - 3
```
- γ2 > 0 (**Leptokurtic**): Heavy-tailed distribution. Outliers are frequent and extreme (typical during database lock escalation or retry storms).

---

## 5. Time Windows & Moving Averages: Sizing the "Last X over Y" Equation

When configuring dashboards, Prometheus queries, and alerting rules, engineering teams must evaluate metrics across two distinct time dimensions:

> **Query Template**: *"Metric over the last **X** (duration) bucketed / compared over **Y** (resolution or baseline)"*

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                     THE "LAST X OVER Y" DUAL-WINDOW MATRIX                  │
├────────────────────┬─────────────────────────────┬──────────────────────────┤
│ PARAMETER          │ DEFINITION                  │ WHAT HAPPENS IF MISSIZED │
├────────────────────┼─────────────────────────────┼──────────────────────────┤
│ X                  │ The Evaluation Window       │ • Too small: Flapping and│
│ (Evaluation Range) │ (e.g. "Last 5 minutes",     │   false-alarm alerts.    │
│                    │ "Last 1 hour", "Last 30d"). │ • Too large: High inertia│
│                    │ Determines historical depth.│   hides brief outages.   │
├────────────────────┼─────────────────────────────┼──────────────────────────┤
│ Y                  │ The Step / Bucket Resolution│ • Too large: Aliasing /  │
│ (Step / Baseline)  │ (e.g. "every 15 seconds",   │   smoothing hides spikes.│
│                    │ "1-minute buckets", "1w").  │ • Too small: High CPU and│
│                    │ Determines granularity.     │   visual noise.          │
└────────────────────┴─────────────────────────────┴──────────────────────────┘
```

---

### 5.1 The "Last X over Y" Dual-Window Framework

1. **X (Evaluation Horizon / Lookback Duration)**:
   Controls how much history is included in the statistical calculation (e.g. `rate(http_requests_total[5m])`).
2. **Y (Step Resolution / Sampling Epoch)**:
   Controls how frequently data points are rendered on the chart or evaluated by the alert engine (e.g. step = `15s`).

---

### 5.2 Comprehensive Sizing Matrix for Operational Tasks

| Operational Task | Optimal X (Evaluation Window) | Optimal Y (Step / Resolution) | Rationale & Practical Example |
| :--- | :--- | :--- | :--- |
| **Real-Time Incident Alerting** | **X = 1m to 5m** | **Y = 15s** | Catches production outages within 60s without waking on-call engineers for a 2-second transient spike. |
| **Micro-Jitter & GC Stalls** | **X = 30s to 60s** | **Y = 100ms to 500ms** | Resolves individual 200ms Stop-The-World GC pauses or lock contention spikes. |
| **Deployment Canary Verification**| **X = 15m to 30m** | **Y = 1m** | Provides sufficient statistical sample size to compare Canary vs Baseline error rate. |
| **Seasonal Anomaly Detection** | **X = 10m** | **Y = 7 days offset** | Evaluates current 10m order rate against the exact matching 10m window from last week. |
| **Capacity & Trend Planning** | **X = 30d to 90d** | **Y = 1 hour to 1 day** | Filters out intra-day noise to expose month-over-month infrastructure growth curves. |

---

### 5.3 The 3 Critical Sizing Pitfalls (Aliasing, Flapping & Inertia)

#### 1. The Aliasing / Information Loss Trap (Y Step Too Large):
- **The Pitfall**: Plotting a 24-hour graph with Y = 1 hour averaging buckets.
- **The Nyquist-Shannon Theorem**: To detect a phenomenon of duration τ, sampling frequency must be at least:
```math
f_s \ge \frac{2}{\tau}
```
- **Consequence**: If a downstream payment gateway suffered a **complete 10-minute total outage** (100% errors) at 14:15, averaging that data over a 1-hour bucket reduces the measured failure rate to ≈ 16.6%. The graph shows a mild bump, completely masking a SEV-1 incident!

#### 2. The Flapping Alert Trap (X Window Too Small):
- **The Pitfall**: Configuring Prometheus alerts with `rate(http_errors[5s]) > 0.05`.
- **Consequence**: A single dropped network packet causes the alert to fire, and 5 seconds later it automatically resolves. The on-call engineer is inundated with 400 pager notifications per night for normal network jitter.

#### 3. The Inertia Delay Trap (X Window Too Large):
- **The Pitfall**: Configuring incident alerts with `rate(http_errors[1h]) > 0.05`.
- **Consequence**: A 10-minute outage triggers the alert. Engineers deploy an immediate hotfix in 2 minutes. However, because the rolling evaluation window X is 60 minutes long, the alert **continues to fire for 50 minutes after the system is 100% healthy**, preventing verification of recovery.

---

### 5.4 Fixed (Tumbling) Windows vs. Sliding Continuous Windows

- **Fixed Window (Tumbling Window)**: Evaluates counts within non-overlapping intervals (e.g. 00:00–00:05, 00:05–00:10). 
  * *Boundary Spike Trap*: If a burst of 1,000 requests arrives between 00:04:50 and 00:05:10, a fixed 5-minute window divides the burst across two buckets (500 in bucket 1, 500 in bucket 2), hiding a rate limit violation.
- **Sliding Continuous Window**: Evaluates metrics over the continuous trailing duration `[t - W, t]`. Accurately captures the full 1,000-request burst regardless of when it lands.

---

### 5.5 Simple Moving Average (SMA) vs. Linear Weighted Moving Average (WMA)

#### 1. Simple Moving Average (SMA):
```math
\text{SMA}_k = \frac{1}{k} \sum_{i=0}^{k-1} x_{t-i}
```
- Weights all k observations equally. Suffers from lag and "ghost jumps" when an old outlier exits the window.

#### 2. Linear Weighted Moving Average (WMA):
```math
\text{WMA}_k = \frac{\sum_{i=1}^{k} i \cdot x_{t-k+i}}{\frac{k(k+1)}{2}}
```
- Assigns linearly increasing weights to newer observations (w_k > w_{k-1} > ... > w_1).

---

### 5.6 Exponential Weighted Moving Average (EWMA) & Half-Life Physics

EWMA applies exponentially decaying weights to all past observations:
```math
S_t = \alpha \cdot x_t + (1 - \alpha) \cdot S_{t-1}
```
Where:
- S_t = Current smoothed estimate.
- x_t = New raw measurement at time t.
- α = Smoothing factor (0 < α <= 1).

#### 1. The Effective Memory Horizon & Half-Life (t_½):
The weight assigned to an observation k steps in the past is w_k = α(1 - α)^k.
The **Half-Life (t_½)** is the number of time steps required for an observation's influence to decay by 50%:
```math
t_{1/2} = \frac{\ln(0.5)}{\ln(1 - \alpha)} \approx \frac{0.693}{\alpha}
```
```
┌─────────────────────────────────────────────────────────────────────────────┐
│                 EWMA SMOOTHING FACTOR (alpha) CALIBRATION TABLE             │
├───────────┬──────────────┬──────────────────────────────────────────────────┤
│ alpha VAL │ HALF-LIFE    │ OPERATIONAL USE CASE                             │
├───────────┼──────────────┼──────────────────────────────────────────────────┤
│ 0.50      │ 1.0 sample   │ Ultra-fast incident detection (< 3 seconds)      │
│ 0.20      │ 3.1 samples  │ Adaptive Limiter (TCP Vegas) gradient tracking   │
│ 0.05      │ 13.5 samples │ Smooth baseline background tracking              │
│ 0.01      │ 69.0 samples │ Multi-hour diurnal traffic trend smoothing       │
└───────────┴──────────────┴──────────────────────────────────────────────────┘
```

---

## 6. Seasonality, Trends & Relative Anomaly Detection

### 6.1 Time-Series Decomposition: Additive vs. Multiplicative Models

Any operational production metric Y(t) can be decomposed into three underlying components:

```
RAW METRIC (Y)        =   TREND (Long-Term Growth)  +  SEASONALITY (Weekly Cycle)  +  RESIDUALS (Noise/Incidents)
 ──/\_/\_/\_/\──────>     ──────────────/─────────>    ─/\_/\_/\_/\_/\─────────>      ──_--__--_--───────>
```

#### 1. Additive Model:
```math
Y(t) = \text{Trend}(t) + \text{Seasonality}(t) + \text{Residuals}(t)
```
- Use when seasonal variations are constant in absolute magnitude regardless of total traffic volume.

#### 2. Multiplicative Model:
```math
Y(t) = \text{Trend}(t) \times \text{Seasonality}(t) \times \text{Residuals}(t)
```
- Use when seasonal swings scale proportionally with overall traffic volume (e.g. peak hours always double baseline traffic).

---

### 6.2 Relative Anomaly Detection: Measuring Percentage Deviations

Static alerting thresholds (e.g. `traffic < 500 RPS`) fail because 200 RPS is a severe outage at 14:00 on Tuesday, but perfectly normal at 04:00 on Sunday.

**Relative Measurement Formula**:
```math
\text{Deviation}_{\%} = \frac{Y(t) - Y_{\text{baseline}}(t)}{Y_{\text{baseline}}(t)} \times 100\%
```
Where Y_baseline(t) is the metric measured at the **exact same hour and day of the previous week(s)**:
```math
Y_{\text{baseline}}(t) = Y(t - 7\text{ days})
```
---

### 6.3 Seasonally-Aware Prometheus Alerting via the `offset` Pattern

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

      - alert: LatencyElevatedRelativeSeasonality
        # Alert if p95 latency is 50% higher than same hour last week
        expr: |
          (
            histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket[10m])) by (le))
            -
            histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket[10m] offset 1w)) by (le))
          )
          /
          histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket[10m] offset 1w)) by (le))
          > 0.50
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "p95 latency is 50% higher than expected for this time of week"
```

---

### 6.4 Holt-Winters Triple Exponential Smoothing (Level, Trend & Seasonality)

The Holt-Winters algorithm models dynamic time series using three recurrence equations:

1. **Level Equation (ℓ_t)** (Smoothed value):
```math
\ell_t = \alpha (y_t - s_{t-m}) + (1 - \alpha)(\ell_{t-1} + b_{t-1})
```
2. **Trend Equation (b_t)** (Smoothed slope):
```math
b_t = \beta (\ell_t - \ell_{t-1}) + (1 - \beta)b_{t-1}
```
3. **Seasonal Equation (s_t)** (Cyclical adjustment for period m):
```math
s_t = \gamma (y_t - \ell_{t-1} - b_{t-1}) + (1 - \gamma)s_{t-m}
```
#### Forecasting Ahead h Time Steps:
```math
\hat{y}_{t+h|t} = \ell_t + h \cdot b_t + s_{t+h-m}
```

Where:
- α, β, γ are smoothing constants for level, trend, and seasonality (0 < α, β, γ <= 1).
- m is the period length (e.g. m = 24 hours or m = 7 days).

#### Prometheus Native Linear Prediction (`predict_linear`):
For short-term trend projection without full triple smoothing:

```promql
predict_linear(node_filesystem_free_bytes[4h], 24 * 3600) < 0
```

*Application*: Alerts on disk space exhaustion 24 hours in advance by evaluating the 4-hour derivative trend.
