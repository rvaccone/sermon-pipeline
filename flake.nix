{
  description = "Sermon pipeline: turns a recorded service into publish-ready sermon outputs";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs = { self, nixpkgs }:
    let
      system = "aarch64-darwin";
      pkgs = nixpkgs.legacyPackages.${system};

      # whisper.cpp 1.9.4 is the first release with --carry-initial-prompt, which keeps punctuation
      # from drifting away on long recordings. Core ML is off: it is optional, and its library fails
      # to link with the nixpkgs toolchain on recent macOS. SDL2 is off because it only enables the
      # microphone examples, one of which downloads llama.cpp at configure time.
      whisper-cpp = (pkgs.whisper-cpp.override { coreMLSupport = false; }).overrideAttrs (old: rec {
        version = "1.9.4";
        src = pkgs.fetchFromGitHub {
          owner = "ggml-org";
          repo = "whisper.cpp";
          rev = "v${version}";
          hash = "sha256-xAFPZnRF0U4o3H46h7q11YEn3mGt3eZk9xDtyOwIz6U=";
        };
        cmakeFlags = (old.cmakeFlags or [ ]) ++ [ "-DWHISPER_SDL2=OFF" ];
      });
    in
    {
      packages.${system}.whisper-cpp = whisper-cpp;

      # mkShellNoCC: no Nix C toolchain in the shell, so the Swift vision helper builds with Xcode's
      # own toolchain and Apple's frameworks.
      devShells.${system}.default = pkgs.mkShellNoCC {
        packages = [
          pkgs.go
          pkgs.gopls
          pkgs.ffmpeg-full
          pkgs.imagemagick
          pkgs.sherpa-onnx
          whisper-cpp
        ];
      };
    };
}
