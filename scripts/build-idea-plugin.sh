#!/bin/bash
set -euo pipefail
project_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$project_root"
export IDEA_HOME="${IDEA_HOME:-$HOME/Applications/IntelliJ IDEA.app}"
python3 - <<'PY'
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import xml.etree.ElementTree as ET
import zipfile

root = Path.cwd()
idea = Path(os.environ['IDEA_HOME']).expanduser()
if idea.suffix == '.app':
    idea /= 'Contents'
product = json.loads((idea / 'Resources/product-info.json').read_text())
if product['buildNumber'].split('.')[0] not in {'261', '262'}:
    raise SystemExit('此适配器支持 IDEA 261 / 262。')
vim = Path(os.environ.get('IDEAVIM_HOME', str(Path.home() / 'Library/Application Support/JetBrains' / product['dataDirectoryName'] / 'plugins/IdeaVIM'))).expanduser()
versions = [v for v in ('2.47.3', '2.48.0') if (vim / f'lib/IdeaVIM-{v}.jar').is_file()]
if not versions:
    raise SystemExit('需要已安装的 IdeaVim 2.47.3 或 2.48.0；通过 IDEAVIM_HOME 指定目录。')
jbr = idea / 'jbr/Contents/Home/bin'
compiler = [str(jbr / 'javac')] if (jbr / 'javac').is_file() else [str(jbr / 'java'), '-m', 'jdk.compiler']
source = root / 'plugins/jetbrains/ime-pilot'
version = ET.parse(source / 'resources/META-INF/plugin.xml').getroot().findtext('version')
scratch = root / '.tmp/idea-plugin'
scratch.mkdir(parents=True, exist_ok=True)
with tempfile.TemporaryDirectory(dir=scratch) as folder:
    stage = Path(folder)
    classes = stage / 'classes'
    classes.mkdir()
    jars = sorted((idea / 'lib').glob('*.jar')) + sorted((vim / 'lib').rglob('*.jar'))
    classpath = os.pathsep.join(map(str, jars))
    sources = sorted(map(str, (source / 'src').rglob('*.java')))
    subprocess.run(compiler + ['--release', '21', '-encoding', 'UTF-8', '-cp', classpath, '-d', str(classes)] + sources, check=True)
    shutil.copytree(source / 'resources', classes, dirs_exist_ok=True)
    jar = stage / 'ime-pilot.jar'
    with zipfile.ZipFile(jar, 'w', zipfile.ZIP_DEFLATED) as archive:
        for path in sorted(classes.rglob('*')):
            if path.is_file():
                archive.write(path, path.relative_to(classes))
    archive_path = stage / f'ime-pilot-idea-{version}.zip'
    with zipfile.ZipFile(archive_path, 'w', zipfile.ZIP_DEFLATED) as archive:
        archive.write(jar, 'ime-pilot/lib/ime-pilot.jar')
    output = root / 'dist' / archive_path.name
    output.parent.mkdir(exist_ok=True)
    os.replace(archive_path, output)
print(f'已构建：{output}')
print(f'编译环境：IDEA {product["version"]} / IdeaVim {versions[0]}')
print('未下载依赖、运行单测、安装插件或修改 IDE 配置。')
PY
