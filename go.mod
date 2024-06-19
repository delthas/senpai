module git.sr.ht/~delthas/senpai

go 1.18

require (
	git.sr.ht/~emersion/go-scfg v0.0.0-20231004133111-9dce55c8d63b
	git.sr.ht/~rockorager/vaxis v0.8.5
	github.com/delthas/go-libnp v0.0.0-20221222161248-0e45ece1f878
	github.com/delthas/go-localeinfo v0.0.0-20221116001557-686a1e185118
	github.com/gdamore/tcell/v2 v2.6.1-0.20230327043120-47ec3a77754f
	github.com/godbus/dbus/v5 v5.1.0
	github.com/mattn/go-runewidth v0.0.15
	golang.org/x/net v0.18.0
	golang.org/x/time v0.4.0
	mvdan.cc/xurls/v2 v2.5.0
)

require (
	github.com/containerd/console v1.0.3 // indirect
	github.com/mattn/go-sixel v0.0.5 // indirect
	github.com/rivo/uniseg v0.4.4 // indirect
	github.com/soniakeys/quant v1.0.0 // indirect
	golang.org/x/image v0.13.0 // indirect
	golang.org/x/sys v0.14.0 // indirect
)

replace github.com/gdamore/tcell/v2 => git.sr.ht/~delthas/vaxis-tcell v0.4.8-0.20240531132546-8a798f9059aa

replace git.sr.ht/~rockorager/vaxis => git.sr.ht/~delthas/vaxis v0.8.6-0.20240617102656-146503a7e4f2
