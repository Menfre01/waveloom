# typed: false
# frozen_string_literal: true

# Homebrew formula for Waveloom
# This file is the canonical source; release workflow syncs it to homebrew-tap.
#
# Installation:
#   brew install Menfre01/tap/waveloom
#
# Or test locally:
#   brew install --formula .github/homebrew/waveloom.rb

class Waveloom < Formula
  desc "Terminal-based coding agent optimized for DeepSeek prefix caching"
  homepage "https://github.com/Menfre01/waveloom"
  version "0.1.0-alpha.4"
  license "Apache-2.0"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/Menfre01/waveloom/releases/download/v#{version}/waveloom_darwin_arm64.tar.gz"
      sha256 "0c39652670e9878bc0b2e5cc4d845326720d15610fdd62ac98e7c232f30a738a"
    else
      url "https://github.com/Menfre01/waveloom/releases/download/v#{version}/waveloom_darwin_amd64.tar.gz"
      sha256 "e852b0023fd251c1c58219df9d06dbc41cb1bd55aa12c3b4a7be22981e4545e1"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/Menfre01/waveloom/releases/download/v#{version}/waveloom_linux_arm64.tar.gz"
      sha256 "c878e12c7b14f773592bc0c87f182e1de1c0eaa15ed8639dbd644f0f3a9a188b"
    else
      url "https://github.com/Menfre01/waveloom/releases/download/v#{version}/waveloom_linux_amd64.tar.gz"
      sha256 "28d045684de1b8b0800e06264ccdb4f9f0e2f746bbd605d726f899797452fe02"
    end
  end

  def install
    bin.install "waveloom"
  end

  test do
    assert_match "waveloom version #{version}", shell_output("#{bin}/waveloom --version")
  end
end
