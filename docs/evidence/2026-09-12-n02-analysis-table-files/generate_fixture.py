import datetime as dt
import hashlib
import io
import json
import sys
from pathlib import Path

import openpyxl

from kb_offline.contracts.analysis_table import AnalysisTableArtifact
from kb_offline.operators.structured_parser import summarize_xlsx


output = Path(sys.argv[1]).resolve()
output.mkdir(parents=True, exist_ok=True)
book = openpyxl.Workbook()
sheet = book.active
sheet.title = "Revenue"
sheet.append(["Region", "Amount", "Active", "Booked At", "Note"])
for index in range(1, 1206):
    sheet.append([
        "sales",
        1000000.25 if index == 1205 else 0.01,
        index % 2 == 0,
        dt.date(2026, 9, (index % 28) + 1),
        None if index % 5 == 0 else f"row-{index}",
    ])
stream = io.BytesIO()
book.save(stream)
book.close()
data = stream.getvalue()
(output / "revenue.xlsx").write_bytes(data)

tables = []
summarize_xlsx(
    data,
    tenant_id="team",
    kb_id="kb",
    doc_id="doc-1",
    doc_version=2,
    title="Revenue workbook",
    analysis_tables=tables,
)
assert len(tables) == 1
assert tables[0]["row_count"] == 1205
assert tables[0]["complete"] is True
artifact = AnalysisTableArtifact(
    tenant_id="team",
    kb_id="kb",
    doc_id="doc-1",
    doc_version=2,
    generation="gen_current",
    content_hash=hashlib.sha256(data).hexdigest(),
    tables=tables,
)
(output / "analysis-table-artifact.json").write_text(
    json.dumps(artifact.model_dump(), ensure_ascii=False, separators=(",", ":")),
    encoding="utf-8",
)
print(json.dumps({
    "xlsx_sha256": hashlib.sha256(data).hexdigest(),
    "artifact_sha256": hashlib.sha256(
        (output / "analysis-table-artifact.json").read_bytes()).hexdigest(),
    "dataset_key": tables[0]["dataset_key"],
    "definition_version": tables[0]["definition_version"],
    "data_version": tables[0]["data_version"],
    "rows": tables[0]["row_count"],
    "column_types": [column["type"] for column in tables[0]["columns"]],
}, ensure_ascii=False, indent=2))
