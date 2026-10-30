package nix

// ModuleHint maps a package attribute to the NixOS/home-manager module
// option that is usually a better choice than installing the package
// directly. For example, programs.git.enable pulls in git, adds shell
// completion, and lets you configure it declaratively.
var ModuleHint = map[string]string{
	// Programs
	"git":               "programs.git.enable",
	"zsh":               "programs.zsh.enable",
	"fish":              "programs.fish.enable",
	"bash":              "programs.bash.enable (home-manager)",
	"tmux":              "programs.tmux.enable",
	"neovim":            "programs.neovim.enable",
	"neovim-unwrapped":  "programs.neovim.enable",
	"firefox":           "programs.firefox.enable",
	"firefox-unwrapped": "programs.firefox.enable",
	"chromium":          "programs.chromium.enable",
	"gpg":               "programs.gnupg.agent.enable",
	"gnupg":             "programs.gnupg.agent.enable",
	"ssh":               "programs.ssh.startAgent or services.openssh.enable",
	"openssh":           "services.openssh.enable",
	"htop":              "programs.htop.enable (home-manager)",
	"direnv":            "programs.direnv.enable",
	"starship":          "programs.starship.enable",
	"alacritty":         "programs.alacritty.enable (home-manager)",
	"kitty":             "programs.kitty.enable (home-manager)",
	"rofi":              "programs.rofi.enable (home-manager)",
	"waybar":            "programs.waybar.enable (home-manager)",
	"mangohud":          "programs.mangohud.enable (home-manager)",
	"obs-studio":        "programs.obs-studio.enable (home-manager)",
	"mpv":               "programs.mpv.enable (home-manager)",

	// Services and virtualisation
	"docker":          "virtualisation.docker.enable",
	"podman":          "virtualisation.podman.enable",
	"libvirtd":        "virtualisation.libvirtd.enable",
	"virt-manager":    "virtualisation.libvirtd.enable + programs.virt-manager.enable",
	"steam":           "programs.steam.enable",
	"gamemode":        "programs.gamemode.enable",
	"flatpak":         "services.flatpak.enable",
	"pipewire":        "services.pipewire.enable",
	"pulseaudio":      "services.pulseaudio.enable (or pipewire)",
	"bluetooth":       "hardware.bluetooth.enable",
	"bluez":           "hardware.bluetooth.enable",
	"cups":            "services.printing.enable",
	"avahi":           "services.avahi.enable",
	"tailscale":       "services.tailscale.enable",
	"syncthing":       "services.syncthing.enable",
	"postgresql":      "services.postgresql.enable",
	"mysql":           "services.mysql.enable",
	"redis":           "services.redis.servers.<name>.enable",
	"nginx":           "services.nginx.enable",
	"caddy":           "services.caddy.enable",
	"fail2ban":        "services.fail2ban.enable",
	"wireguard-tools": "networking.wireguard.enable or networking.wg-quick",
	"networkmanager":  "networking.networkmanager.enable",
	"ufw":             "networking.firewall.enable (built in)",
	"iptables":        "networking.firewall.enable (built in)",
	"nftables":        "networking.nftables.enable",
	"sway":            "programs.sway.enable",
	"hyprland":        "programs.hyprland.enable",

	// Fonts
	"nerdfonts":           "fonts.packages (add them to the list, not to systemPackages)",
	"noto-fonts":          "fonts.packages",
	"noto-fonts-cjk-sans": "fonts.packages",
}
