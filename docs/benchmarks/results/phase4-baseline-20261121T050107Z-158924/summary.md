# Phase 4 benchmark: baseline

- Run ID (UTC): 20261121T050107Z-158924
- Gateway URL: http://localhost:8082
- Profile: baseline
- Offered load: 100/s for 30s
- Unique identities in target file: 1
- Host kernel: Linux 6.18.58.1-microsoft-standard-WSL2 x86_64 GNU/Linux
- Logical CPUs: 18
- Go: go version go1.27.4 linux/amd64
- Vegeta build: /home/sanjana/go/bin/vegeta: go1.27.4;	path	github.com/tsenart/vegeta/v12;	mod	github.com/tsenart/vegeta/v12	v12.13.0	h1:J/UiNS3f69MkL0tsRLVUUV8uXXQZxdRUchtS+GYiSFc=;	dep	github.com/beorn7/perks	v1.0.1	h1:VlbKKnNfV8bJzeqoa4cOKqO6bYr3WgKZxO8Z16+hsOM=;	dep	github.com/c2h5oh/datasize	v0.0.0-20231215233829-aa82cc1e6500	h1:6lhrsTEnloDPXyeZBvSYvQf8u86jbKehZPVDDlkgDl4=;
- Redis: not used by the no-op HTTP baseline
- Git revision: 91222abc438b96bf1e008e88a71bae7d69cf7197

- Wall-clock benchmark duration (seconds): 30
- Redis snapshots: not applicable to baseline

Requests      [total, rate, throughput]         3000, 100.03, 100.03
Duration      [total, attack, wait]             29.991s, 29.99s, 686.843µs
Latencies     [min, mean, 50, 90, 95, 99, max]  334.182µs, 654.214µs, 647.45µs, 788.72µs, 853.809µs, 1.067ms, 3.053ms
Bytes In      [total, mean]                     371674, 123.89
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           100.00%
Status Codes  [code:count]                      200:3000  
Error Set:
