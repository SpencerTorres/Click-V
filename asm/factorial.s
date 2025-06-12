.section .bss
buffer: 
    .skip 32             # Reserve 32 bytes for input/output buffer

.section .text

.global _start

_start:
    la      a0, buffer
    jal     ra, read_stdin
    jal	    ra, atoi
    jal     ra, factorial

    # Convert result integer to ASCII string
    mv      a1, a0             # move length to a1 (itoa expects number in a1)
    la      a0, buffer         # Load address of output buffer into a0
    jal     ra, itoa           # Convert integer in a1 to ASCII string stored at a

    # Reverse
    mv 	    a1, a0
    la      a0, buffer
    jal     ra, reverse

    # Print ASCII 
    jal     ra, print  	       # Print the buffer

# ---------------------------
# read_stdin
# Reads up to 32 bytes from stdin into the buffer pointed to by a0.
#
# Arguments:
#   a0 - address of the input buffer to store data from stdin
#
# Returns:
#   a0 - unchanged; address of the input buffer
#   a1 - number of bytes actually read from stdin
#
# Clobbers:
#   a0, a1, a2, a7, t0
read_stdin:
    mv      t0, a0
    li      a7, 13       # syscall number for read
    li      a0, 0        # file descriptor: stdin
    mv      a1, t0       # buffer to read into
    li      a2, 32       # max number of bytes to read
    ecall
    
    mv      a1, a0             # return number of bytes read in a1
    mv      a0, t0             # return buffer address in a0
    jr      ra

# ---------------------------
# atoi
# Converts ASCII digits at address in a0 to integer
# Stops at newline ('\n')
# Returns: a0 = integer result
atoi:
    li      t4, 0x0A           # newline character '\n'
    li      t1, 0              # initialize result to 0

atoi_loop:
    lbu     t2, 0(a0)          # load byte from buffer
    beq     t2, t4, atoi_done  # stop at newline
    addi    t2, t2, -48        # convert ASCII to integer (subtract '0')
    mul     t1, t1, t4         # result *= 10
    add     t1, t1, t2         # result += digit
    addi    a0, a0, 1          # advance to next character
    j       atoi_loop

atoi_done:
    mv      a0, t1             # move result to a0 (return value)
    jr      ra

   
# ---------------------------
# itoa
# Converts integer in a1 to ASCII string at address in a0
# Digits are written in reverse order
# Returns: a0 = number of digits written
itoa:
    li      t0, 10             # base 10 divisor
    li      t1, 1              # counter for digits written
    mv      t2, a0             # t2 = write pointer

itoa_loop:
    rem     t3, a1, t0         # t3 = a1 % 10 (last digit)
    addi    t3, t3, 48         # convert digit to ASCII
    div     a1, a1, t0         # a1 = a1 / 10
    sb      t3, 0(t2)          # store byte to buffer
    beq     a1, zero, itoa_done # if number is now 0, we're done
    addi    t2, t2, 1          # advance buffer
    addi    t1, t1, 1          # increment digit count
    j       itoa_loop

itoa_done:
    mv      a0, t1             # return length of ASCII string
    jr      ra

print:
    li      a7, 1              # syscall number for print
    ecall                      # write character

    jr      ra

factorial:
	li	t0, 1          # t0 stores the result
	li	t1, 1          # t1 is the stop criteria
	
factorial_loop:
	beq	a0, t1, factorial_done
	mul	t0, t0, a0
	addi 	a0, a0, -1
	j	factorial_loop

factorial_done:
	mv	a0, t0
	jr 	ra

reverse:
    mv      t0, a0         # t0 = start pointer
    add     t1, a0, a1     # t1 = a0 + length
    addi    t1, t1, -1     # move to last real character (ignore newline)

reverse_loop:
    ble     t1, t0, reverse_done
    
    # swap
    lbu     t2, 0(t0)
    lbu     t3, 0(t1) 
    sb      t3, 0(t0)
    sb      t2, 0(t1)

    addi    t0, t0, 1
    addi    t1, t1, -1

    j       reverse_loop

reverse_done:
    jr      ra
