class TelegramJsonl < Formula
  desc "Archive Telegram messages as JSONL and download selected attachments"
  homepage "https://github.com/ObsidianArch02/telegram-jsonl"
  license "AGPL-3.0-only"

  head do
    url "https://github.com/ObsidianArch02/telegram-jsonl.git", branch: "main"
    depends_on "go" => :build
  end

  def install
    ENV["CGO_ENABLED"] = "0"
    system "go", "build", *std_go_args(ldflags: "-s -w -X main.version=#{version}")
    pkgshare.install "LICENSE", "NOTICE", "go.mod", "go.sum", "docs", "licenses"
    pkgshare.install Dir["*.md"]
  end

  test do
    assert_match "telegram-jsonl", shell_output("#{bin}/telegram-jsonl --version")
    assert_match "Usage:", shell_output("#{bin}/telegram-jsonl --help")
  end
end
