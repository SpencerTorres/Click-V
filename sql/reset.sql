---------------------------------------
-- Reset + Load Program for Click-V
---------------------------------------

-- Reset PC to 0
INSERT INTO clickv.pc (value) VALUES (0);

-- Clear registers, reset to 0
TRUNCATE TABLE clickv.registers SYNC;
INSERT INTO clickv.registers (address, value) SELECT number AS address, 0 AS value FROM numbers(1 + 31);

-- Clear memory, define new memory layout
-- 2Kib ROM, 1Kib RAM. 800b VRAM. Initialize to 0.
TRUNCATE TABLE clickv.memory SYNC;
-- INSERT INTO clickv.memory (address, value) SELECT number AS address, 0 AS value FROM numbers((1536*1024));

-- Clear console
TRUNCATE TABLE clickv.print;

-- Reset ClickOS (Optional)
-- SELECT clickos_syscall(0, []);

-- Paste actual program here. Whitespace is removed automatically.
INSERT INTO clickv.load_program (hex) VALUES ('
  
  
');
