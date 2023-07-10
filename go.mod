module git.sr.ht/~taiite/senpai

go 1.16

require (
	git.sr.ht/~emersion/go-scfg v0.0.0-20201019143924-142a8aa629fc
	github.com/delthas/go-libnp v0.0.0-20221222161248-0e45ece1f878
	github.com/delthas/go-localeinfo v0.0.0-20221116001557-686a1e185118
	github.com/gdamore/tcell/v2 v2.6.1-0.20230327043120-47ec3a77754f
	github.com/mattn/go-runewidth v0.0.14
	golang.org/x/net v0.0.0-20220722155237-a158d28d115b
	golang.org/x/term v0.5.0
	golang.org/x/time v0.0.0-20210611083556-38a9dc6acbc6
	mvdan.cc/xurls/v2 v2.3.0
)

replace github.com/gdamore/tcell/v2 => github.com/delthas/tcell/v2 v2.4.1-0.20230710100648-1489e78d90fb
