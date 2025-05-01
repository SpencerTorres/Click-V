_start:
        addi t0, zero, 0
        addi t1, zero, 0xFF
        addi t3, zero, 10
        jal zero, loop

loop:
        lh t2, 0(zero)
        lb t4, 4(zero)
        lw t5, 8(zero)
        add t0, t0, t1
        sh t0, 0(zero)
        sb t0, 4(zero)
        sw t0, 8(zero)
        jal zero, loop

end:
        jal zero, end


_start:
        addi a7, zero, 1
        la a0, msg
        addi a1, zero, 12
        ecall

msg:
        .ascii "clickhouse!"
