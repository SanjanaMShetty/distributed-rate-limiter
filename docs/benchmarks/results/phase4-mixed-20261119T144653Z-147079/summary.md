# Phase 4 benchmark: mixed

- Run ID (UTC): 20261119T144653Z-147079
- Gateway URL: http://localhost:8080
- Profile: mixed
- Offered load: 100/s for 30s
- Unique identities in target file: 1
- Host kernel: Linux 6.18.58.1-microsoft-standard-WSL2 x86_64 GNU/Linux
- Logical CPUs: 18
- Go: go version go1.27.4 linux/amd64
- Vegeta build: /home/sanjana/go/bin/vegeta: go1.27.4;	path	github.com/tsenart/vegeta/v12;	mod	github.com/tsenart/vegeta/v12	v12.13.0	h1:J/UiNS3f69MkL0tsRLVUUV8uXXQZxdRUchtS+GYiSFc=;	dep	github.com/beorn7/perks	v1.0.1	h1:VlbKKnNfV8bJzeqoa4cOKqO6bYr3WgKZxO8Z16+hsOM=;	dep	github.com/c2h5oh/datasize	v0.0.0-20231215233829-aa82cc1e6500	h1:6lhrsTEnloDPXyeZBvSYvQf8u86jbKehZPVDDlkgDl4=;
- Redis: Redis server v=7.4.12 sha=00000000:0 malloc=jemalloc-5.3.0 bits=64 build=97c9b7ac751c8f00
- Git revision: 91222abc438b96bf1e008e88a71bae7d69cf7197

- Wall-clock benchmark duration (seconds): 31
- Redis INFO stats and memory snapshots: redis-*-before.txt / redis-*-after.txt

Requests      [total, rate, throughput]         3000, 100.03, 53.31
Duration      [total, attack, wait]             29.992s, 29.99s, 1.355ms
Latencies     [min, mean, 50, 90, 95, 99, max]  605.151µs, 1.239ms, 1.224ms, 1.458ms, 1.596ms, 2.292ms, 4.604ms
Bytes In      [total, mean]                     431706, 143.90
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           53.30%
Status Codes  [code:count]                      200:1599  429:1401  
Error Set:
429 Too Many Requests
