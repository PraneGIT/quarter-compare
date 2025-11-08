package main

import (
	"encoding/json"
	"fmt"
	"html"
	"math"
	"os"
	"sort"
	"strings"
)

// helper: format percent with sign and two decimals, "N/A" if NaN or missing
func fmtPercentChange(curr, prev float64) string {
	if math.IsNaN(curr) || math.IsNaN(prev) {
		return "N/A"
	}
	if prev == 0 {
		// avoid showing infinite — present a readable hint
		return "N/A (prev=0)"
	}
	pct := (curr - prev) / math.Abs(prev) * 100.0
	return fmt.Sprintf("%.2f%%", pct)
}

// color class for percent: positive -> green, negative -> red, neutral -> lightgray
func pctColorClass(curr, prev float64) string {
	if math.IsNaN(curr) || math.IsNaN(prev) {
		return "neutral"
	}
	if prev == 0 {
		if curr == 0 {
			return "neutral"
		}
		if curr > 0 {
			return "positive"
		}
		return "negative"
	}
	pct := (curr - prev) / math.Abs(prev) * 100.0
	if pct > 0.5 {
		return "positive"
	}
	if pct < -0.5 {
		return "negative"
	}
	return "neutral"
}

// avg of slice ignoring NaN; returns NaN if no valid values
func avgFloats(vals []float64) float64 {
	sum := 0.0
	count := 0
	for _, v := range vals {
		if !math.IsNaN(v) {
			sum += v
			count++
		}
	}
	if count == 0 {
		return math.NaN()
	}
	return sum / float64(count)
}

// GenerateHTMLReport writes a simple HTML comparing companies
func GenerateHTMLReport(path string, results []CompanyResult) error {
	// determine quarters header using first non-empty CompanyResult
	headerQuarters := []string{"Q1", "Q2", "Q3", "Q4"}
	for _, r := range results {
		for i, q := range r.Quarters {
			if q != "" {
				headerQuarters[i] = q
			}
		}
		break
	}

	var sb strings.Builder
	sb.WriteString("<!doctype html><html><head><meta charset='utf-8'><title>Quarter Compare</title>")
	sb.WriteString(`<style>
body{font-family:Arial,Helvetica,sans-serif}
table{border-collapse:collapse;width:100%}td,th{border:1px solid #ccc;padding:6px;text-align:right}
th{background:#f2f2f2;text-align:center;cursor:pointer;user-select:none}
td.left{text-align:left}
.positive{background:#d4edda} .negative{background:#f8d7da} .neutral{background:#fffbe6}
.highlight{box-shadow:inset 0 0 0 3px #ffd54f}
.summary{margin-top:20px;padding:10px;border:1px solid #ddd;background:#fafafa}
.small{font-size:0.9em;color:#666}
tr:hover{background:#f0f8ff}
.sort-indicator{margin-left:6px;font-size:0.8em;color:#666}
</style>`)

	// small JS sorter: uses data-sort attribute when present, toggles asc/desc per column
	sb.WriteString(`<script>
document.addEventListener("DOMContentLoaded", function(){
  const table = document.getElementById("reportTable");
  if(!table) return;
  const ths = table.querySelectorAll("thead th");
  ths.forEach(function(th, idx){
    // do not attach to the first column (company) if you still want sorting; we attach to all
    th.addEventListener("click", function(){
      const curDir = th.getAttribute("data-dir") || "desc";
      const newDir = curDir === "desc" ? "asc" : "desc";
      // reset indicators
      ths.forEach(function(x){ x.setAttribute("data-dir",""); const sp=x.querySelector(".sort-indicator"); if(sp) sp.textContent=""; });
      th.setAttribute("data-dir", newDir);
      const indicator = th.querySelector(".sort-indicator");
      if(indicator) indicator.textContent = newDir==="asc"?"▲":"▼";
      sortTable(table, idx, newDir==="asc");
    });
  });
});

function parseNumericCell(cell){
  const ds = cell.getAttribute("data-sort");
  if(ds !== null && ds.length>0){
    const n = Number(ds);
    if(!isNaN(n)) return n;
  }
  // fallback: try strip % and commas
  const txt = cell.textContent.replace(/%/g,'').replace(/,/g,'').trim();
  const n = Number(txt);
  if(!isNaN(n)) return n;
  return NaN;
}

function sortTable(table, colIndex, asc){
  const tbody = table.tBodies[0];
  const rows = Array.from(tbody.rows);
  rows.sort(function(a,b){
    const aCell = a.cells[colIndex];
    const bCell = b.cells[colIndex];
    const aVal = parseNumericCell(aCell);
    const bVal = parseNumericCell(bCell);
    const aNan = Number.isNaN(aVal);
    const bNan = Number.isNaN(bVal);
    if(aNan && bNan) return 0;
    if(aNan) return 1; // push NaN to bottom
    if(bNan) return -1;
    if(aVal < bVal) return asc ? -1 : 1;
    if(aVal > bVal) return asc ? 1 : -1;
    // tie-breaker: company name (first cell)
    const aName = a.cells[0].textContent.trim().toLowerCase();
    const bName = b.cells[0].textContent.trim().toLowerCase();
    return aName < bName ? -1 : (aName > bName ? 1 : 0);
  });
  // re-append rows in new order
  rows.forEach(function(r){ tbody.appendChild(r); });
}
</script>`)

	sb.WriteString("</head><body>")
	sb.WriteString("<h2>Quarterly Revenue & Net Profit comparison</h2>")
	// build table with id for JS
	sb.WriteString("<table id='reportTable'><thead><tr><th>Company <span class='sort-indicator'></span></th>")
	for _, q := range headerQuarters {
		sb.WriteString("<th colspan='2'>" + html.EscapeString(q) + " <span class='sort-indicator'></span></th>")
	}
	// Last-2 percent columns (explicit)
	sb.WriteString("<th>Last-2 %Δ Rev <span class='sort-indicator'></span></th><th>Last-2 %Δ NP <span class='sort-indicator'></span></th>")
	// avg3 change columns
	sb.WriteString("<th>Δ Avg3 Rev <span class='sort-indicator'></span></th><th>Δ Avg3 NP <span class='sort-indicator'></span></th>")
	sb.WriteString("</tr><tr><th></th>")
	for range headerQuarters {
		sb.WriteString("<th>Revenue</th><th>Net Profit</th>")
	}
	sb.WriteString("<th></th><th></th><th></th><th></th>")
	sb.WriteString("</tr></thead><tbody>")

	// collect overall stats
	type statRow struct {
		Company         string
		RevPct          float64
		NPPct           float64
		Avg3RevChange   float64
		Avg3NPChange    float64
		RevenueLatest   float64
		NetProfitLatest float64
	}
	var stats []statRow
	notDeclaredCount := 0

	for _, r := range results {
		// embed per-row JSON (company, longName, quarters, revenue nums, netprofit nums)
		jsObj := map[string]interface{}{
			"company":   r.Company,
			"longName":  r.LongName,
			"quarters":  r.Quarters,
			"revenue":   r.RevenueNums,
			"netprofit": r.NetProfitNums,
		}
		jb, _ := json.Marshal(jsObj)
		sb.WriteString("<tr data-json='" + html.EscapeString(string(jb)) + "'>")

		sb.WriteString("<td class='left'>" + html.EscapeString(r.Company) + "<br/><span class='small'>" + html.EscapeString(r.LongName) + "</span></td>")

		// revenue & netprofit cells
		for i := 0; i < 4; i++ {
			rv := "not declared"
			np := "not declared"
			rvNum := math.NaN()
			npNum := math.NaN()
			if i < len(r.Revenue) && string(r.Revenue[i]) != "" {
				rv = string(r.Revenue[i])
				rvNum = r.RevenueNums[i]
			}
			if i < len(r.NetProfit) && string(r.NetProfit[i]) != "" {
				np = string(r.NetProfit[i])
				npNum = r.NetProfitNums[i]
			}
			if math.IsNaN(rvNum) {
				notDeclaredCount++
			}
			// revenue cell
			sb.WriteString("<td data-sort='" + numSortValue(rvNum) + "'>" + html.EscapeString(rv) + "</td>")
			// netprofit cell
			sb.WriteString("<td data-sort='" + numSortValue(npNum) + "'>" + html.EscapeString(np) + "</td>")
		}

		// calculate latest vs previous % (Last-2 %Δ)
		latestRev := math.NaN()
		prevRev := math.NaN()
		latestNP := math.NaN()
		prevNP := math.NaN()
		if len(r.RevenueNums) > 0 {
			latestRev = r.RevenueNums[0]
		}
		if len(r.RevenueNums) > 1 {
			prevRev = r.RevenueNums[1]
		}
		if len(r.NetProfitNums) > 0 {
			latestNP = r.NetProfitNums[0]
		}
		if len(r.NetProfitNums) > 1 {
			prevNP = r.NetProfitNums[1]
		}
		revPctNum := pctOrNaN(latestRev, prevRev)
		npPctNum := pctOrNaN(latestNP, prevNP)
		revPctStr := fmtPercentChange(latestRev, prevRev)
		npPctStr := fmtPercentChange(latestNP, prevNP)
		revClass := pctColorClass(latestRev, prevRev)
		npClass := pctColorClass(latestNP, prevNP)

		// compute avg last3 and prev3 change when possible (use positions: [0,1,2] and [1,2,3])
		avg3Rev := math.NaN()
		avgPrev3Rev := math.NaN()
		avg3NP := math.NaN()
		avgPrev3NP := math.NaN()
		if len(r.RevenueNums) >= 3 {
			avg3Rev = avgFloats(r.RevenueNums[0:min(3, len(r.RevenueNums))])
		}
		if len(r.RevenueNums) >= 4 {
			avgPrev3Rev = avgFloats(r.RevenueNums[1:4])
		}
		if len(r.NetProfitNums) >= 3 {
			avg3NP = avgFloats(r.NetProfitNums[0:min(3, len(r.NetProfitNums))])
		}
		if len(r.NetProfitNums) >= 4 {
			avgPrev3NP = avgFloats(r.NetProfitNums[1:4])
		}
		avg3RevPctNum := pctOrNaN(avg3Rev, avgPrev3Rev)
		avg3NPPctNum := pctOrNaN(avg3NP, avgPrev3NP)
		avg3RevPctStr := fmtPercentChange(avg3Rev, avgPrev3Rev)
		avg3NPPctStr := fmtPercentChange(avg3NP, avgPrev3NP)
		avg3Class := "neutral"
		if !math.IsNaN(avg3Rev) && !math.IsNaN(avgPrev3Rev) && avgPrev3Rev != 0 {
			if (avg3Rev-avgPrev3Rev)/math.Abs(avgPrev3Rev) > 0.5 {
				avg3Class = "positive highlight"
			} else if (avg3Rev-avgPrev3Rev)/math.Abs(avgPrev3Rev) < -0.5 {
				avg3Class = "negative highlight"
			}
		}

		// Last-2 %Δ columns with numeric data-sort for sorting
		sb.WriteString("<td class='" + revClass + "' data-sort='" + numSortValue(revPctNum) + "' style='font-weight:600;text-align:center'>" + html.EscapeString(revPctStr) + "</td>")
		sb.WriteString("<td class='" + npClass + "' data-sort='" + numSortValue(npPctNum) + "' style='font-weight:600;text-align:center'>" + html.EscapeString(npPctStr) + "</td>")
		// avg3 columns
		sb.WriteString("<td class='" + avg3Class + "' data-sort='" + numSortValue(avg3RevPctNum) + "' style='text-align:center'>" + html.EscapeString(avg3RevPctStr) + "</td>")
		sb.WriteString("<td class='" + avg3Class + "' data-sort='" + numSortValue(avg3NPPctNum) + "' style='text-align:center'>" + html.EscapeString(avg3NPPctStr) + "</td>")

		sb.WriteString("</tr>")

		stats = append(stats, statRow{
			Company:         r.Company,
			RevPct:          revPctNum,
			NPPct:           npPctNum,
			Avg3RevChange:   avg3RevPctNum,
			Avg3NPChange:    avg3NPPctNum,
			RevenueLatest:   latestRev,
			NetProfitLatest: latestNP,
		})
	}
	sb.WriteString("</tbody></table>")

	// Modal HTML (hidden by default) and tooltip container
	sb.WriteString(`<div id="modalOverlay" style="display:none;position:fixed;left:0;top:0;width:100%;height:100%;background:rgba(0,0,0,0.5);z-index:9999;">
  <div id="modal" style="background:#fff;width:900px;max-width:95%;margin:60px auto;padding:16px;border-radius:6px;position:relative;">
    <button id="modalClose" style="position:absolute;right:10px;top:10px;padding:6px 10px;">Close</button>
    <h3 id="modalTitle"></h3>
    <div style="display:flex;gap:16px;flex-wrap:wrap;">
      <div style="flex:1 1 400px;min-width:260px;">
        <canvas id="revenueChart" class="chart-canvas" style="width:100%;height:240px;border:1px solid #eee;display:block"></canvas>
      </div>
      <div style="flex:1 1 400px;min-width:260px;">
        <canvas id="profitChart" class="chart-canvas" style="width:100%;height:240px;border:1px solid #eee;display:block"></canvas>
      </div>
    </div>
    <div id="chartTooltip" style="position:absolute;pointer-events:none;display:none;background:#fff;padding:6px;border:1px solid #ccc;border-radius:4px;box-shadow:0 2px 6px rgba(0,0,0,0.15);font-size:12px;z-index:10000"></div>
    <div id="modalNote" class="small" style="margin-top:8px;color:#555"></div>
  </div>
</div>`)

	// existing overall analysis block preserved
	sb.WriteString("<div class='summary'><h3>Overall analysis</h3>")
	if len(stats) == 0 {
		sb.WriteString("<p>No companies processed.</p>")
	} else {
		// best/worst movers by RevPct and NPPct (ignore NaN)
		// find top by RevPct
		revStats := []statRow{}
		npStats := []statRow{}
		avg3Stats := []statRow{}
		for _, s := range stats {
			if !math.IsNaN(s.RevPct) {
				revStats = append(revStats, s)
			}
			if !math.IsNaN(s.NPPct) {
				npStats = append(npStats, s)
			}
			if !math.IsNaN(s.Avg3RevChange) {
				avg3Stats = append(avg3Stats, s)
			}
		}
		if len(revStats) > 0 {
			sort.Slice(revStats, func(i, j int) bool { return revStats[i].RevPct > revStats[j].RevPct })
			bestRev := revStats[0]
			worstRev := revStats[len(revStats)-1]
			sb.WriteString("<p><strong>Total companies:</strong> " + fmt.Sprintf("%d", len(results)) + "</p>")
			sb.WriteString("<p><strong>Not-declared data points observed:</strong> " + fmt.Sprintf("%d", notDeclaredCount) + "</p>")
			sb.WriteString("<p><strong>Top revenue mover (latest %Δ):</strong> " + html.EscapeString(bestRev.Company) + " — " + fmt.Sprintf("%.2f%%", bestRev.RevPct) + "</p>")
			sb.WriteString("<p><strong>Worst revenue mover (latest %Δ):</strong> " + html.EscapeString(worstRev.Company) + " — " + fmt.Sprintf("%.2f%%", worstRev.RevPct) + "</p>")
		} else {
			sb.WriteString("<p>No valid latest revenue %Δ values for summary (most prev==0 or missing).</p>")
		}
		if len(npStats) > 0 {
			sort.Slice(npStats, func(i, j int) bool { return npStats[i].NPPct > npStats[j].NPPct })
			sb.WriteString("<p><strong>Top profit mover (latest %Δ):</strong> " + html.EscapeString(npStats[0].Company) + " — " + fmt.Sprintf("%.2f%%", npStats[0].NPPct) + "</p>")
		}
		if len(avg3Stats) > 0 {
			sort.Slice(avg3Stats, func(i, j int) bool { return avg3Stats[i].Avg3RevChange > avg3Stats[j].Avg3RevChange })
			sb.WriteString("<p><strong>Highest Avg3 Revenue change:</strong> " + html.EscapeString(avg3Stats[0].Company) + " — " + fmt.Sprintf("%.2f%%", avg3Stats[0].Avg3RevChange) + "</p>")
		}
		// some aggregate metrics: average revenue change across companies (ignore NaN)
		sumRev := 0.0
		countRev := 0
		sumNP := 0.0
		countNP := 0
		for _, s := range stats {
			if !math.IsNaN(s.RevPct) {
				sumRev += s.RevPct
				countRev++
			}
			if !math.IsNaN(s.NPPct) {
				sumNP += s.NPPct
				countNP++
			}
		}
		if countRev > 0 {
			avgRevPct := sumRev / float64(countRev)
			sb.WriteString("<p><strong>Average latest %Δ Revenue across companies:</strong> " + fmt.Sprintf("%.2f%%", avgRevPct) + "</p>")
		}
		if countNP > 0 {
			avgNPPct := sumNP / float64(countNP)
			sb.WriteString("<p><strong>Average latest %Δ NetProfit across companies:</strong> " + fmt.Sprintf("%.2f%%", avgNPPct) + "</p>")
		}
	}
	sb.WriteString("</div>")

	// Updated JS: responsive canvas, DPR scaling, redraw on hover, tooltip.
	sb.WriteString(`<script>
// helper: setup canvas for devicePixelRatio
function setupCanvasForDPR(canvas){
  const dpr = window.devicePixelRatio || 1;
  const styleW = canvas.clientWidth;
  const styleH = canvas.clientHeight;
  canvas.width = Math.round(styleW * dpr);
  canvas.height = Math.round(styleH * dpr);
  const ctx = canvas.getContext("2d");
  ctx.setTransform(dpr,0,0,dpr,0,0); // scale coordinates to CSS pixels
  return ctx;
}

// drawChart: draws full chart, optionally highlight index
function drawChart(canvas, labels, values, title, highlightIndex){
  const ctx = setupCanvasForDPR(canvas);
  const cw = canvas.clientWidth;
  const ch = canvas.clientHeight;
  // clear
  ctx.clearRect(0,0,cw,ch);
  // padding
  const padLeft = 40, padRight = 20, padTop = 30, padBottom = 40;
  const chartW = cw - padLeft - padRight;
  const chartH = ch - padTop - padBottom;

  // numeric array and compute min/max ignoring NaN
  const nums = [];
  for(let i=0;i<values.length;i++){
    const v = values[i];
    const n = (v === null || v === undefined || isNaN(Number(v))) ? NaN : Number(v);
    nums.push(n);
  }
  let min = Infinity, max = -Infinity;
  for(const v of nums){ if(!isNaN(v)){ min=Math.min(min,v); max=Math.max(max,v); } }
  if(min===Infinity || max===-Infinity){
    ctx.fillStyle="#666";
    ctx.font="14px Arial";
    ctx.fillText("No numeric data to display", padLeft, padTop + 20);
    return;
  }
  // add small margins
  if (min === max) { min = min - Math.abs(min)*0.05 - 1; max = max + Math.abs(max)*0.05 + 1; }
  const range = max - min;

  // axes
  ctx.strokeStyle = "#ddd";
  ctx.lineWidth = 1;
  ctx.beginPath();
  // y grid lines and labels
  ctx.fillStyle = "#666";
  ctx.font = "11px Arial";
  const gridLines = 4;
  for(let i=0;i<=gridLines;i++){
    const y = padTop + (chartH * i / gridLines);
    ctx.beginPath();
    ctx.moveTo(padLeft, y);
    ctx.lineTo(padLeft + chartW, y);
    ctx.stroke();
    const val = (max - (range * i / gridLines));
    ctx.fillText(val.toFixed(2), 4, y+4);
  }
  // x-axis labels placeholders
  const n = nums.length;
  const stepX = n>1 ? chartW / (n-1) : chartW;
  // draw line
  ctx.beginPath();
  ctx.strokeStyle = "#2c7be5";
  ctx.lineWidth = 2;
  let firstDrawn = false;
  for(let i=0;i<n;i++){
    const v = nums[i];
    if(isNaN(v)) continue;
    const x = padLeft + i * stepX;
    const y = padTop + chartH - ((v - min) / range) * chartH;
    if(!firstDrawn){ ctx.moveTo(x,y); firstDrawn = true; } else { ctx.lineTo(x,y); }
  }
  ctx.stroke();
  // draw points and labels
  for(let i=0;i<n;i++){
    const v = nums[i];
    const x = padLeft + i * stepX;
    const y = isNaN(v) ? padTop + chartH : padTop + chartH - ((v - min) / range) * chartH;
    // x label
    const lab = labels[i] || "";
    ctx.fillStyle = "#333";
    ctx.font = "11px Arial";
    ctx.fillText(lab, x - 20, padTop + chartH + 16, 80);
    if(!isNaN(v)){
      ctx.beginPath();
      ctx.fillStyle = (i===highlightIndex) ? "#ff6b6b" : "#2c7be5";
      ctx.arc(x, y, (i===highlightIndex)?6:4, 0, Math.PI*2);
      ctx.fill();
      // small value near point
      ctx.fillStyle = "#000";
      ctx.font = "11px Arial";
      if (i===highlightIndex) {
        ctx.fillText(v.toString(), x+8, y-8);
      }
    } else {
      // draw hollow marker for missing
      ctx.beginPath();
      ctx.strokeStyle = "#bbb";
      ctx.arc(x, padTop + chartH, 3, 0, Math.PI*2);
      ctx.stroke();
    }
  }
  // title
  ctx.fillStyle="#111";
  ctx.font="bold 13px Arial";
  ctx.fillText(title, padLeft, 16);
  // store computed points for hover interactions
  const pts = [];
  for(let i=0;i<n;i++){
    const px = padLeft + i * stepX;
    const py = isNaN(nums[i]) ? padTop + chartH : padTop + chartH - ((nums[i] - min) / range) * chartH;
    pts.push({x:px,y:py,val:nums[i],label:labels[i]||""});
  }
  canvas._chartPoints = pts;
}

// utility: get mouse pos in CSS pixels relative to canvas
function getMousePos(canvas, evt){
  const rect = canvas.getBoundingClientRect();
  const x = evt.clientX - rect.left;
  const y = evt.clientY - rect.top;
  return {x:x, y:y};
}

// attach hover handlers to canvas
function attachHover(canvas, titlePrefix){
  if(!canvas) return;
  // remove existing listeners (simple approach)
  canvas.onmousemove = null;
  canvas.onmouseleave = null;
  const tooltip = document.getElementById("chartTooltip");
  canvas.onmousemove = function(e){
    const pos = getMousePos(canvas, e);
    const pts = canvas._chartPoints || [];
    let nearest = -1;
    let minDist = 1e9;
    for(let i=0;i<pts.length;i++){
      const d = Math.hypot(pos.x - pts[i].x, pos.y - pts[i].y);
      if(d < minDist){ minDist = d; nearest = i; }
    }
    // consider radius threshold (20px)
    if(minDist <= 20 && nearest >= 0){
      // redraw with highlight
      const allLabels = pts.map(p=>p.label);
      const allVals = pts.map(p=>p.val);
      drawChart(canvas, allLabels, allVals, titlePrefix, nearest);
      // show tooltip near cursor
      const p = pts[nearest];
      tooltip.style.display = "block";
      tooltip.style.left = (e.clientX + 12) + "px";
      tooltip.style.top = (e.clientY + 12) + "px";
      tooltip.innerHTML = "<strong>"+ (p.label || "") + "</strong><br/>" + (isNaN(p.val) ? "N/A" : p.val);
    } else {
      // no highlight
      const allLabels = pts.map(p=>p.label);
      const allVals = pts.map(p=>p.val);
      drawChart(canvas, allLabels, allVals, titlePrefix, -1);
      tooltip.style.display = "none";
    }
  };
  canvas.onmouseleave = function(){
    const pts = canvas._chartPoints || [];
    const allLabels = pts.map(p=>p.label);
    const allVals = pts.map(p=>p.val);
    drawChart(canvas, allLabels, allVals, titlePrefix, -1);
    const tooltip = document.getElementById("chartTooltip");
    tooltip.style.display = "none";
  };
}

// open modal helper existing in code: ensure we call attachHover after initial draw
document.addEventListener("DOMContentLoaded", function(){
  const table = document.getElementById("reportTable");
  if(!table) return;
  const rows = table.tBodies[0].rows;
  for(let r of rows){
    r.style.cursor = "pointer";
    r.addEventListener("click", function(e){
      // open modal with row
      const j = r.getAttribute("data-json");
      if(!j) return;
      let obj;
      try { obj = JSON.parse(j); } catch(err){ console.error("invalid row json", err); return; }
      const title = obj.company + " — " + (obj.longName || "");
      document.getElementById("modalTitle").textContent = title;
      const quarters = obj.quarters || [];
      const revenue = obj.revenue || [];
      const profit = obj.netprofit || [];
      document.getElementById("modalNote").textContent = "Hover over points to see values. Showing up to 4 quarters.";
      const revCanvas = document.getElementById("revenueChart");
      const profCanvas = document.getElementById("profitChart");
      drawChart(revCanvas, quarters, revenue, "Revenue", -1);
      drawChart(profCanvas, quarters, profit, "Net Profit", -1);
      attachHover(revCanvas, "Revenue");
      attachHover(profCanvas, "Net Profit");
      document.getElementById("modalOverlay").style.display = "block";
    });
  }
  const closeBtn = document.getElementById("modalClose");
  if(closeBtn) closeBtn.addEventListener("click", function(){ document.getElementById("modalOverlay").style.display = "none"; document.getElementById("chartTooltip").style.display = "none"; });
});
</script>`)

	// write file
	return os.WriteFile(path, []byte(sb.String()), 0644)
}

// helper: return percent as float64 or NaN
func pctOrNaN(curr, prev float64) float64 {
	if math.IsNaN(curr) || math.IsNaN(prev) {
		return math.NaN()
	}
	if prev == 0 {
		// treat as NaN so it's excluded from numeric summaries and sorting
		return math.NaN()
	}
	return (curr - prev) / math.Abs(prev) * 100.0
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// numSortValue converts a float64 into a string for data-sort attribute
func numSortValue(v float64) string {
	if math.IsNaN(v) {
		return ""
	}
	// use sufficient precision
	return fmt.Sprintf("%.6f", v)
}
