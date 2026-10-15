import {describe, expect, it} from 'vitest';
import {strToU8, zipSync} from 'fflate';
import {
  assembleSingleHtml,
  buildEmbeddedLicenseComment,
  escapeHtml,
  escapeScriptText,
  packageSb3ToHtml,
  removePercentSection,
  validateExportFilename,
  validateSb3File
} from '../src/scratch-export/packager-core';

describe('packager-core unit tests', () => {
  describe('validateExportFilename', () => {
    it('infers filename from fallbackBase when empty', () => {
      expect(validateExportFilename('', '我的作品')).toBe('我的作品.html');
      expect(validateExportFilename('   ', '太空漫游')).toBe('太空漫游.html');
      expect(validateExportFilename('', '')).toBe('Scratch作品.html');
    });

    it('throws error when filename is empty and fallbackBase contains illegal characters', () => {
      expect(() => validateExportFilename('', '<script>alert(1)</script>')).toThrow(
        '作品名包含非法字符，请显式填写合法导出文件名'
      );
      expect(() => validateExportFilename('', 'folder/project')).toThrow(
        '作品名包含非法字符，请显式填写合法导出文件名'
      );
      expect(() => validateExportFilename('', '..')).toThrow(
        '作品名包含非法字符，请显式填写合法导出文件名'
      );
    });

    it('accepts explicit clean filename even when fallbackBase has special characters', () => {
      expect(validateExportFilename('game.html', '<script>alert(1)</script>')).toBe('game.html');
      expect(validateExportFilename('clean_name', '<script>alert(1)</script>')).toBe('clean_name.html');
    });

    it('keeps existing .html extension or appends it', () => {
      expect(validateExportFilename('game.html', 'default')).toBe('game.html');
      expect(validateExportFilename('game.htm', 'default')).toBe('game.htm');
      expect(validateExportFilename('game', 'default')).toBe('game.html');
    });

    it('throws error when filename contains path separators', () => {
      expect(() => validateExportFilename('foo/bar.html', 'default')).toThrow('路径分隔符');
      expect(() => validateExportFilename('..\\secret.html', 'default')).toThrow('路径分隔符');
      expect(() => validateExportFilename('/tmp/game', 'default')).toThrow('路径分隔符');
    });

    it('throws error when filename is . or ..', () => {
      expect(() => validateExportFilename('.', 'default')).toThrow('不能为 "." 或 ".."');
      expect(() => validateExportFilename('..', 'default')).toThrow('不能为 "." 或 ".."');
    });

    it('throws error when filename contains illegal or control characters', () => {
      expect(() => validateExportFilename('game<test>.html', 'default')).toThrow('非法字符');
      expect(() => validateExportFilename('project:1.html', 'default')).toThrow('非法字符');
      expect(() => validateExportFilename('game*star.html', 'default')).toThrow('非法字符');
      expect(() => validateExportFilename('hello?world.html', 'default')).toThrow('非法字符');
      expect(() => validateExportFilename('test|pipe.html', 'default')).toThrow('非法字符');
      expect(() => validateExportFilename('name\x00null.html', 'default')).toThrow('非法字符');
    });
  });

  describe('escaping and cleaning utilities', () => {
    it('escapes HTML special characters', () => {
      const raw = '<script>alert("1" & \'2\')</script>';
      const escaped = escapeHtml(raw);
      expect(escaped).toBe('&lt;script&gt;alert(&quot;1&quot; &amp; &#39;2&#39;)&lt;/script&gt;');
    });

    it('escapes script closing tags', () => {
      const raw = 'var x = "</script><script>alert(1)</script>";';
      const escaped = escapeScriptText(raw);
      expect(escaped).toBe('var x = "<\\/script><script>alert(1)<\\/script>";');
    });

    it('removes percent sections cleanly', () => {
      const tpl = 'before % remove % should be gone % /remove % after';
      expect(removePercentSection(tpl, 'remove')).toBe('before  after');
    });
  });

  describe('embedded license comment and third-party notices', () => {
    it('contains MIT license terms and full THIRD_PARTY_NOTICES declaration', () => {
      const comment = buildEmbeddedLicenseComment();
      expect(comment).toContain('Copyright (c) 2026 凉介');
      expect(comment).toContain('Copyright (c) 2020 Sean');
      expect(comment).toContain('MIT License');
      expect(comment).toContain('Third-Party Notices (THIRD_PARTY_NOTICES.md)');
      expect(comment).toContain('29f52618abaa41a060862bd3981f846ab4b249ece56762cd2423e8048b1a732e');
      expect(comment).not.toContain('-->\n-->'); // Ensure properly formed
      expect(comment.startsWith('<!--')).toBe(true);
      expect(comment.trim().endsWith('-->')).toBe(true);
    });
  });

  describe('packageSb3ToHtml', () => {
    it('rejects payload exceeding 50MB at entry', async () => {
      // Mock Uint8Array with byteLength > 50MB
      const oversizedBytes = {
        byteLength: 51 * 1024 * 1024
      } as unknown as Uint8Array;

      await expect(
        packageSb3ToHtml(oversizedBytes, {
          sourceMode: 'current',
          title: '超限作品',
          filename: 'overflow.html',
          description: '',
          compatibleMode: true,
          turboMode: false,
          runDefault: false
        })
      ).rejects.toThrow('超过 50MB 上限');
    });
  });

  describe('assembleSingleHtml', () => {
    it('correctly replaces placeholders without dollar-pattern corruption', () => {
      const mockTemplate = `<!DOCTYPE html>
<html>
<head>
<title>{TITLE}</title>
<meta name="description" content="{DESCRIPTION}">
<style>
.ratio { padding-bottom: {HEIGHT/WIDTH%}%; }
.inv { padding-bottom: {WIDTH/HEIGHT%}%; }
</style>
</head>
<body>
% no-vm % <script src="remote.js"></script> % /no-vm %
% loading-image % <img src="loading.png"> % /loading-image %
<script>{SCRIPTS}</script>
</body>
</html>`;

      // Special string with $, $&, $1, $', $` that would break non-function .replace()
      const trickyTitle = 'Game $& $1 $\' $$ with Special $ Symbols';
      const trickyDesc = 'Desc with $& $2 $` chars';
      const trickyVmCode = 'console.log("vm $& $1 $2");';

      const html = assembleSingleHtml({
        template: mockTemplate,
        vmCode: trickyVmCode,
        projectDataUrl: 'data:application/octet-stream;base64,AAAA',
        title: trickyTitle,
        description: trickyDesc,
        compatibleMode: true,
        turboMode: false,
        runDefault: false,
        width: 480,
        height: 360
      });

      expect(html).toContain('<!DOCTYPE html>');
      expect(html).toContain('Copyright (c) 2026 凉介');
      expect(html).toContain('Third-Party Notices (THIRD_PARTY_NOTICES.md)');
      expect(html).toContain(escapeHtml(trickyTitle));
      expect(html).toContain(escapeHtml(trickyDesc));
      expect(html).toContain('console.log("vm $& $1 $2");');
      expect(html).not.toContain('{SCRIPTS}');
      expect(html).not.toContain('remote.js');
      expect(html).not.toContain('loading.png');
    });
  });

  describe('validateSb3File', () => {
    it('rejects files larger than 50MB', async () => {
      const fakeFile = {
        name: 'test.sb3',
        size: 51 * 1024 * 1024,
        arrayBuffer: async () => new ArrayBuffer(10)
      } as unknown as File;

      await expect(validateSb3File(fakeFile)).rejects.toThrow('超过 50MB 上限');
    });

    it('rejects files without .sb3 extension', async () => {
      const fakeFile = {
        name: 'test.sb2',
        size: 100,
        arrayBuffer: async () => new ArrayBuffer(10)
      } as unknown as File;

      await expect(validateSb3File(fakeFile)).rejects.toThrow('仅支持 .sb3 格式');
    });

    it('rejects non-zip files', async () => {
      const buffer = new Uint8Array([0x00, 0x01, 0x02, 0x03]).buffer;
      const fakeFile = {
        name: 'corrupt.sb3',
        size: 4,
        arrayBuffer: async () => buffer
      } as unknown as File;

      await expect(validateSb3File(fakeFile)).rejects.toThrow('不是有效的 SB3 (ZIP) 格式');
    });

    it('rejects 6-byte fake zip files that have valid header but no central directory or project.json', async () => {
      const buffer = new Uint8Array([0x50, 0x4b, 0x03, 0x04, 0x00, 0x00]).buffer;
      const fakeFile = {
        name: 'fake6byte.sb3',
        size: 6,
        arrayBuffer: async () => buffer
      } as unknown as File;

      await expect(validateSb3File(fakeFile)).rejects.toThrow('不是有效的 SB3 (ZIP) 格式');
    });

    it('rejects ordinary valid zip without project.json', async () => {
      const zipData = zipSync({
        'other-file.txt': strToU8('not a scratch project')
      });
      const fakeFile = {
        name: 'regular.sb3',
        size: zipData.byteLength,
        arrayBuffer: async () => zipData.buffer
      } as unknown as File;

      await expect(validateSb3File(fakeFile)).rejects.toThrow('缺少 project.json');
    });

    it('rejects valid zip with invalid non-JSON project.json', async () => {
      const zipData = zipSync({
        'project.json': strToU8('{ this is broken json')
      });
      const fakeFile = {
        name: 'badjson.sb3',
        size: zipData.byteLength,
        arrayBuffer: async () => zipData.buffer
      } as unknown as File;

      await expect(validateSb3File(fakeFile)).rejects.toThrow('无法解析为有效 JSON');
    });

    it('rejects valid zip with project.json missing targets array', async () => {
      const zipData = zipSync({
        'project.json': strToU8(JSON.stringify({name: 'No Targets'}))
      });
      const fakeFile = {
        name: 'notargets.sb3',
        size: zipData.byteLength,
        arrayBuffer: async () => zipData.buffer
      } as unknown as File;

      await expect(validateSb3File(fakeFile)).rejects.toThrow('缺少 targets 列表');
    });

    it('accepts valid Scratch project fixture with targets array', async () => {
      const validProject = {
        targets: [
          {
            isStage: true,
            name: 'Stage',
            variables: {},
            lists: {},
            broadcasts: {},
            blocks: {},
            comments: {},
            currentCostume: 0,
            costumes: [],
            sounds: []
          }
        ],
        monitors: [],
        extensions: [],
        meta: {semver: '3.0.0', vm: '0.2.0'}
      };
      const zipData = zipSync({
        'project.json': strToU8(JSON.stringify(validProject))
      });
      const fakeFile = {
        name: 'valid-fixture.sb3',
        size: zipData.byteLength,
        arrayBuffer: async () => zipData.buffer
      } as unknown as File;

      const bytes = await validateSb3File(fakeFile);
      expect(bytes.byteLength).toBe(zipData.byteLength);
      expect(bytes[0]).toBe(0x50);
      expect(bytes[1]).toBe(0x4b);
    });
  });
});
