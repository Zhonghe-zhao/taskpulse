# Preliminary Dispatch Results

These results came from the current MySQL-backed TaskPulse deployment with
40 tasks and a fixed fake execution delay of approximately 30 seconds.

| Worker count | Duration | Throughput |
| ---: | ---: | ---: |
| 2 | 601.595 s | 0.06649 tasks/s |
| 4 | 300.993 s | 0.13289 tasks/s |

## Interpretation

Doubling the Worker count approximately doubled throughput and halved total
duration. This is consistent with the task execution time, rather than the
MySQL Claim path, being the dominant cost in this workload.

This is evidence against introducing Redis solely because the task count is
large. It is not yet a complete database bottleneck experiment because the
run did not record MySQL CPU, lock waits, connection usage, or the new claim
attempt/miss counters.

## Required follow-up

Repeat the same matrix with `cmd/dispatch-benchmark`, record the Prometheus
claim counters before and after each run, and collect MySQL resource metrics.
Only compare a Redis design if MySQL claim latency or database resource usage
becomes the measured limiting factor.
