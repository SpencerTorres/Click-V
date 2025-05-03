
------------------------------------------------------------------------------------------------------------------------------------------------------------
------------------------------------------------------------------------------------------------------------------------------------------------------------
------------------------------------------------------------------------------------------------------------------------------------------------------------
-- Run + Control Click-V
------------------------------------------------------------------------------------------------------------------------------------------------------------
------------------------------------------------------------------------------------------------------------------------------------------------------------
------------------------------------------------------------------------------------------------------------------------------------------------------------

---------------------------------------
-- Click-V Cheat Sheet
---------------------------------------

-- Step clock
INSERT INTO clickv.clock (_) VALUES ();

-- Show program
SELECT * FROM clickv.display_program;

-- Show registers
SELECT * FROM clickv.display_registers;

-- Show memory (with o parameter for offset)
SELECT * FROM clickv.display_memory(o=1024);

-- Show Console / Printed messages
SELECT * FROM clickv.display_console FORMAT RawBLOB;

-- Frame
SET allow_experimental_live_view = 1;
	-- Show Frame
		SELECT * FROM clickv.display_frame FORMAT RawBLOB;
	-- Live-update frame
		WATCH clickv.display_frame FORMAT RawBLOB;

-- Other option to show frame:
WITH
  -- add 1 to end to fix end of frame.
  range(1, 800 + 1, 1) AS cell_iter,
  (SELECT groupArray(value) FROM (SELECT address, value FROM clickv.memory WHERE address >= 0x00000C00 AND address < (0x00000C00 + 800) ORDER BY address ASC)) AS cells
SELECT
  concat(
    concat(char(27), '[2J', char(27), '[1;1H'),
    arrayStringConcat(arrayMap(i -> concat(char(27), '[', toString(arrayElement(cells, i)), 'm', '█', if(i % 40 = 0, '\n', '')), cell_iter))
    ) AS d FORMAT RawBLOB;
