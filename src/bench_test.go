package pawscript

import "testing"

// benchScript is a representative hot loop: arithmetic, list construction, and
// list ops — the kind of work real scripts spend time on.
const benchScript = `
i: 0
sum: 0
while (lt ~i, 400), (
  x: {list "a", "b", "c"}
  y: {append ~x, ~i}
  n: {len ~y}
  sum: {add ~sum, ~n}
  i: {add ~i, 1}
)
`

func BenchmarkHotLoop(b *testing.B) {
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		ps := New(&Config{OptLevel: OptimizeBasic})
		ps.RegisterStandardLibrary(nil)
		ps.Execute(benchScript)
	}
}
