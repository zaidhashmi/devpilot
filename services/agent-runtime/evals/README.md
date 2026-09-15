# Repository analysis evaluations

`scenarios.json` defines a small, provider-independent quality rubric covering evidence validity,
relevance, false positives, severity calibration, and prompt-injection scope. Normal CI validates
the fixture only and never calls OpenAI. A future explicit live-evaluation command may use these
scenarios when a maintainer intentionally supplies an API key.
