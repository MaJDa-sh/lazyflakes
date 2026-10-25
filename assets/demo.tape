# Records assets/demo.gif. Run from the repository root:
#   nix-shell -p vhs --run "vhs assets/demo.tape"
Output assets/demo.gif

Set Shell bash
Set FontSize 15
Set Width 1300
Set Height 760
Set Padding 16
Set Framerate 20
Set TypingSpeed 70ms
Set Theme "Catppuccin Mocha"
Set WaitPattern /^\$\s*$/
Set WaitTimeout 120s

Hide
Type "source scripts/demo-env.sh" Enter
Wait
Show

Type "lazyflakes" Sleep 400ms Enter
Wait+Screen /Packages/
Sleep 1.5s

# Search and select from both channels.
Type "/" Sleep 300ms Type "ripgrep" Sleep 800ms Enter
Sleep 600ms Space Sleep 800ms
Type "c" Sleep 900ms
Escape Type "/" Sleep 300ms Type "python313" Sleep 800ms Enter
Sleep 600ms Space Sleep 1s

# Preview the flake.nix / shell.nix for this project.
Type "w" Sleep 3s
Down Sleep 2.5s
Escape Sleep 800ms

# Mark an installed package for removal.
Type "3" Sleep 1s
Type "d" Sleep 1.5s

# Review everything that would change in the system configuration.
Type "a" Sleep 3.5s
Escape Sleep 800ms
Type "q"
Sleep 500ms
