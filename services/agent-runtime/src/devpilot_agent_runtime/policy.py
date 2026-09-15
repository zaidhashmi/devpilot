MAX_EVIDENCE_REQUESTS = 8
MAX_EVIDENCE_PATH_LENGTH = 1024
MAX_EVIDENCE_LINES_PER_REQUEST = 120
MAX_EVIDENCE_REASON_LENGTH = 240

# This is a model-context budget, not the 512 KiB source-broker security ceiling.
# Four KiB is roughly 1,024 tokens and leaves useful room in qwen3's 4,096-token
# context for instructions, schemas, reasoning, and the final structured result.
DEFAULT_ANALYSIS_EVIDENCE_MAX_BYTES = 4 << 10
MIN_ANALYSIS_EVIDENCE_MAX_BYTES = 512
MAX_ANALYSIS_EVIDENCE_MAX_BYTES = 32 << 10
