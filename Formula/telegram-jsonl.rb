class TelegramJsonl < Formula
  desc "Archive Telegram messages as JSONL and fetch attachments on demand"
  homepage "https://github.com/ObsidianArch02/telegram-jsonl"
  version "0.1.0"
  license "AGPL-3.0-only"

  head do
    url "https://github.com/ObsidianArch02/telegram-jsonl.git", branch: "main"
    depends_on "go" => :build
  end

  on_macos do
    on_arm do
      url "https://github.com/ObsidianArch02/telegram-jsonl/releases/download/v0.1.0/telegram-jsonl_v0.1.0_darwin_arm64.tar.gz"
      sha256 "83756c1dc3c99e236390af7686cdb0dca20be461507b8b46f1d4a51c7fbb0bc7"
    end
    on_intel do
      url "https://github.com/ObsidianArch02/telegram-jsonl/releases/download/v0.1.0/telegram-jsonl_v0.1.0_darwin_amd64.tar.gz"
      sha256 "5196a60a515ef05e470d572f2248ffe6e68a9d840cbc270e804208581fa8311d"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/ObsidianArch02/telegram-jsonl/releases/download/v0.1.0/telegram-jsonl_v0.1.0_linux_arm64.tar.gz"
      sha256 "e88a7e25106f85dec6444a58d73cbf94f3f9244864dbfc6d3c5e2c67ff5c752b"
    end
    on_intel do
      url "https://github.com/ObsidianArch02/telegram-jsonl/releases/download/v0.1.0/telegram-jsonl_v0.1.0_linux_amd64.tar.gz"
      sha256 "fb5b132b00ddf99327dfcd2c1f6209e428a29caf92d65f7b15dd213808458ce5"
    end
  end

  def install
    if build.head?
      ENV["CGO_ENABLED"] = "0"
      system "go", "build", *std_go_args(ldflags: "-s -w -X main.version=#{version}")
    else
      bin.install "telegram-jsonl"
    end
    pkgshare.install "LICENSE", "NOTICE", "go.mod", "go.sum", "docs", "licenses"
    pkgshare.install Dir["*.md"]
  end

  test do
    assert_match "telegram-jsonl", shell_output("#{bin}/telegram-jsonl --version")
    assert_match "Usage:", shell_output("#{bin}/telegram-jsonl --help")
  end
end
